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

package brokerquery

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/lindb/lindb/internal/concurrent"
	"github.com/lindb/lindb/internal/linmetric"
	"github.com/lindb/lindb/models"
	protoCommonV1 "github.com/lindb/lindb/proto/gen/v1/common"
	"github.com/lindb/lindb/rpc"
	"github.com/lindb/lindb/sql/stmt"
)

func TestTaskManager_SubmitMetricTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	currentNode := models.Node{IP: "1.1.1.1", Port: 8000}
	taskClientFactory := rpc.NewMockTaskClientFactory(ctrl)
	taskServerFactory := rpc.NewMockTaskServerFactory(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	taskManager1 := NewTaskManager(
		ctx,
		currentNode,
		taskClientFactory,
		taskServerFactory,
		concurrent.NewPool(
			"p",
			10,
			time.Minute,
			linmetric.NewScope("test"),
		),
		time.Second*10,
	)
	physicalPlan := models.NewPhysicalPlan(models.Root{Indicator: "1.1.1.3:8000", NumOfTask: 2})
	physicalPlan.AddLeaf(models.Leaf{
		BaseNode: models.BaseNode{
			Parent:    "1.1.1.3:8000",
			Indicator: "1.1.1.1:9000",
		},
		Receivers: []models.Node{{IP: "1.1.1.1", Port: 2000}},
		ShardIDs:  []int32{1, 2, 4},
	})
	physicalPlan.AddIntermediate(models.Intermediate{
		BaseNode: models.BaseNode{
			Parent:    "1.1.2.3:8000",
			Indicator: "1.1.2.1:9000",
		},
		NumOfTask: 1,
	})
	// no client
	taskClientFactory.EXPECT().GetTaskClient(gomock.Any()).
		Return(nil).Times(1)
	_, _ = taskManager1.SubmitMetricTask(
		physicalPlan, &stmt.Query{})

	// send error
	client := protoCommonV1.NewMockTaskService_HandleClient(ctrl)
	taskClientFactory.EXPECT().GetTaskClient(gomock.Any()).
		Return(client).Times(1)
	client.EXPECT().Send(gomock.Any()).Return(io.ErrClosedPipe)
	_, _ = taskManager1.SubmitMetricTask(
		physicalPlan, &stmt.Query{})

	// send ok
	taskClientFactory.EXPECT().GetTaskClient(gomock.Any()).
		Return(client).Times(2)
	client.EXPECT().Send(gomock.Any()).Return(nil).Times(2)
	_, _ = taskManager1.SubmitMetricTask(
		physicalPlan, &stmt.Query{})

	tm := taskManager1.(*taskManager)
	// task not found
	assert.Error(t, tm.Receive(&protoCommonV1.TaskResponse{
		TaskID: "1.1.1.1:8000"}, ""))
	// task found
	assert.Nil(t, tm.Receive(&protoCommonV1.TaskResponse{
		TaskID: "1.1.1.1:8000-3"}, ""))
}

func TestTaskManager_SendResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	taskServerFactory := rpc.NewMockTaskServerFactory(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	taskManager1 := NewTaskManager(
		ctx,
		models.Node{IP: "1.1.1.1", Port: 8000},
		nil,
		taskServerFactory,
		concurrent.NewPool(
			"p",
			10,
			time.Minute,
			linmetric.NewScope("test"),
		), time.Second)

	// empty stream
	taskServerFactory.EXPECT().GetStream(gomock.Any()).Return(nil)
	assert.Error(t, taskManager1.SendResponse("1", &protoCommonV1.TaskResponse{}))

	// send stream error
	stream := protoCommonV1.NewMockTaskService_HandleServer(ctrl)
	taskServerFactory.EXPECT().GetStream(gomock.Any()).Return(stream).Times(2)
	stream.EXPECT().Send(gomock.Any()).Return(io.ErrClosedPipe)
	assert.Error(t, taskManager1.SendResponse("1", &protoCommonV1.TaskResponse{}))

	// send ok
	stream.EXPECT().Send(gomock.Any()).Return(nil)
	assert.Nil(t, taskManager1.SendResponse("1", &protoCommonV1.TaskResponse{}))
}

func TestTaskManager_SubmitMetaDataTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	currentNode := models.Node{IP: "1.1.1.2", Port: 8000}
	taskClientFactory := rpc.NewMockTaskClientFactory(ctrl)
	taskServerFactory := rpc.NewMockTaskServerFactory(ctrl)

	taskManager2 := NewTaskManager(
		context.Background(),
		currentNode,
		taskClientFactory,
		taskServerFactory,
		concurrent.NewPool(
			"p",
			10,
			time.Minute,
			linmetric.NewScope("test"),
		),
		time.Second*10,
	)

	physicalPlan := models.NewPhysicalPlan(models.Root{Indicator: "1.1.1.3:8000", NumOfTask: 2})
	physicalPlan.AddLeaf(models.Leaf{
		BaseNode: models.BaseNode{
			Parent:    "1.1.1.4:8000",
			Indicator: "1.1.1.2:9000",
		},
		Receivers: []models.Node{{IP: "1.1.1.1", Port: 2000}},
		ShardIDs:  []int32{1, 2, 4},
	})
	// send error
	client := protoCommonV1.NewMockTaskService_HandleClient(ctrl)
	client.EXPECT().Send(gomock.Any()).Return(io.ErrClosedPipe)
	taskClientFactory.EXPECT().GetTaskClient(gomock.Any()).
		Return(client)
	_, err := taskManager2.SubmitMetaDataTask(physicalPlan, &stmt.Metadata{})
	assert.Error(t, err)

	// get client error
	taskClientFactory.EXPECT().GetTaskClient(gomock.Any()).
		Return(nil)
	_, err = taskManager2.SubmitMetaDataTask(physicalPlan, &stmt.Metadata{})
	assert.Error(t, err)

	// SubmitIntermediateMetricTask
	_ = taskManager2.SubmitIntermediateMetricTask(physicalPlan, &stmt.Query{}, "")
}

func TestTaskManager_cleaner(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tm := NewTaskManager(
		context.Background(),
		models.Node{},
		nil,
		nil,
		concurrent.NewPool(
			"p",
			10,
			time.Minute,
			linmetric.NewScope("test"),
		),
		time.Second*10,
	).(*taskManager)
	go tm.cleaner(time.Millisecond * 10)
	task := NewMockTaskContext(ctrl)
	task.EXPECT().Expired(gomock.Any()).Return(true)

	tm.tasks.Store("1", task)
	time.Sleep(time.Second)

}
