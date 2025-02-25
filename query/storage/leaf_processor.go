// Licensed to LinDB under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. LinDB licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package storagequery

import (
	"context"
	"errors"
	"fmt"

	"github.com/lindb/lindb/constants"
	"github.com/lindb/lindb/internal/linmetric"
	"github.com/lindb/lindb/models"
	"github.com/lindb/lindb/pkg/encoding"
	"github.com/lindb/lindb/pkg/logger"
	"github.com/lindb/lindb/pkg/timeutil"
	protoCommonV1 "github.com/lindb/lindb/proto/gen/v1/common"
	"github.com/lindb/lindb/query"
	"github.com/lindb/lindb/rpc"
	"github.com/lindb/lindb/sql/stmt"
	"github.com/lindb/lindb/tsdb"
)

// leafTaskProcessor represents the leaf node's task, the leaf node is always storage node
// 1. receives the task request, and searches the data from time seres engine
// 2. sends the result to the parent node(root or intermediate)
type leafTaskProcessor struct {
	currentNode       models.Node
	currentNodeID     string
	engine            tsdb.Engine
	taskServerFactory rpc.TaskServerFactory
	logger            *logger.Logger

	storageMetricQueryCounter  *linmetric.BoundDeltaCounter
	storageMetaQueryCounter    *linmetric.BoundDeltaCounter
	storageOmitResponseCounter *linmetric.BoundDeltaCounter
}

// NewLeafTaskProcessor creates the leaf task
func NewLeafTaskProcessor(
	currentNode models.Node,
	engine tsdb.Engine,
	taskServerFactory rpc.TaskServerFactory,
) query.TaskProcessor {
	storageQueryScope := linmetric.NewScope("lindb.storage.query")
	return &leafTaskProcessor{
		currentNode:                currentNode,
		currentNodeID:              (&currentNode).Indicator(),
		engine:                     engine,
		taskServerFactory:          taskServerFactory,
		logger:                     logger.GetLogger("query", "LeafTaskDispatcher"),
		storageMetricQueryCounter:  storageQueryScope.NewDeltaCounter("metric_queries"),
		storageMetaQueryCounter:    storageQueryScope.NewDeltaCounter("meta_queries"),
		storageOmitResponseCounter: storageQueryScope.NewDeltaCounter("omitted_responses"),
	}
}

// Process dispatches the request to storage engine query processor
func (p *leafTaskProcessor) Process(
	ctx context.Context,
	stream protoCommonV1.TaskService_HandleServer,
	req *protoCommonV1.TaskRequest,
) {
	err := p.process(ctx, req)
	if err == nil {
		return
	}
	if sendError := stream.Send(&protoCommonV1.TaskResponse{
		TaskID:    req.ParentTaskID,
		Type:      protoCommonV1.TaskType_Leaf,
		Completed: true,
		ErrMsg:    err.Error(),
		SendTime:  timeutil.NowNano(),
	}); sendError != nil {
		p.logger.Error("failed to send error message to target stream",
			logger.String("taskID", req.ParentTaskID),
			logger.Error(err),
		)
	}
}

// Process processes the task request, searches the metric's data from time series engine
func (p *leafTaskProcessor) process(
	ctx context.Context,
	req *protoCommonV1.TaskRequest,
) error {
	physicalPlan := models.PhysicalPlan{}
	if err := encoding.JSONUnmarshal(req.PhysicalPlan, &physicalPlan); err != nil {
		return fmt.Errorf("%w: %s", query.ErrUnmarshalPlan, err)
	}

	foundTask := false
	var curLeaf models.Leaf
	for _, leaf := range physicalPlan.Leafs {
		if leaf.Indicator == p.currentNodeID {
			foundTask = true
			curLeaf = leaf
			break
		}
	}
	if !foundTask {
		p.storageOmitResponseCounter.Incr()
		return fmt.Errorf("%w, i: %s am not a leaf node", query.ErrBadPhysicalPlan, p.currentNodeID)
	}
	db, ok := p.engine.GetDatabase(physicalPlan.Database)
	if !ok {
		p.storageOmitResponseCounter.Incr()
		return fmt.Errorf("%w: %s", query.ErrNoDatabase, physicalPlan.Database)
	}
	stream := p.taskServerFactory.GetStream(curLeaf.Parent)
	if stream == nil {
		p.storageOmitResponseCounter.Incr()
		return fmt.Errorf("%w: %s", query.ErrNoSendStream, curLeaf.Parent)
	}

	switch req.RequestType {
	case protoCommonV1.RequestType_Data:
		p.storageMetricQueryCounter.Incr()
		if err := p.processDataSearch(ctx, db, curLeaf.ShardIDs, req, &curLeaf); err != nil {
			return err
		}
	case protoCommonV1.RequestType_Metadata:
		p.storageMetaQueryCounter.Incr()
		if err := p.processMetadataSuggest(db, curLeaf.ShardIDs, req, stream); err != nil {
			return err
		}
	default:
		p.storageOmitResponseCounter.Incr()
		return nil
	}
	return nil
}

func (p *leafTaskProcessor) processMetadataSuggest(
	db tsdb.Database,
	shardIDs []int32,
	req *protoCommonV1.TaskRequest,
	stream protoCommonV1.TaskService_HandleServer,
) error {
	var stmtQuery = &stmt.Metadata{}
	if err := stmtQuery.UnmarshalJSON(req.Payload); err != nil {
		return query.ErrUnmarshalSuggest
	}
	exec := newStorageMetadataQuery(db, shardIDs, stmtQuery)
	result, err := exec.Execute()
	if err != nil && !errors.Is(err, constants.ErrNotFound) {
		return err
	}
	// send result to upstream
	if err := stream.Send(&protoCommonV1.TaskResponse{
		Type:      protoCommonV1.TaskType_Leaf,
		TaskID:    req.ParentTaskID,
		Completed: true,
		Payload:   encoding.JSONMarshal(&models.SuggestResult{Values: result}),
	}); err != nil {
		return err
	}
	return nil
}

func (p *leafTaskProcessor) processDataSearch(
	ctx context.Context,
	db tsdb.Database,
	shardIDs []int32,
	req *protoCommonV1.TaskRequest,
	leafNode *models.Leaf,
) error {
	stmtQuery := stmt.Query{}
	if err := stmtQuery.UnmarshalJSON(req.Payload); err != nil {
		return query.ErrUnmarshalQuery
	}

	// execute leaf task
	storageExecuteCtx := newStorageExecuteContext(shardIDs, &stmtQuery)
	queryFlow := NewStorageQueryFlow(
		ctx,
		storageExecuteCtx,
		&stmtQuery,
		req,
		p.taskServerFactory,
		leafNode,
		db.ExecutorPool(),
	)
	exec := newStorageMetricQuery(queryFlow, db, storageExecuteCtx)
	exec.Execute()
	return nil
}
