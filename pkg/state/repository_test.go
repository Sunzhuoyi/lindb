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

package state

import (
	"testing"

	"github.com/lindb/lindb/config"
	"github.com/lindb/lindb/internal/mock"

	"github.com/stretchr/testify/assert"
)

func TestNewRepo(t *testing.T) {
	cluster := mock.StartEtcdCluster(t)
	defer cluster.Terminate(t)
	cfg := config.RepoState{
		Endpoints: cluster.Endpoints,
	}

	factory := NewRepositoryFactory("nobody")
	repo, err := factory.CreateRepo(cfg)
	assert.Nil(t, err)
	assert.NotNil(t, repo)
}

func TestEventType_String(t *testing.T) {
	assert.Equal(t, "delete", EventTypeDelete.String())
	assert.Equal(t, "modify", EventTypeModify.String())
	assert.Equal(t, "all", EventTypeAll.String())
	assert.Equal(t, "unknown", EventType(111).String())
}
