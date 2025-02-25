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

package models

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lindb/lindb/pkg/encoding"
	"github.com/lindb/lindb/pkg/option"
)

func TestCreateShardTask_Bytes(t *testing.T) {
	task := CreateShardTask{
		DatabaseName:   "test",
		ShardIDs:       []int32{1, 4, 6},
		DatabaseOption: option.DatabaseOption{},
	}
	data := task.Bytes()
	task1 := CreateShardTask{}
	_ = encoding.JSONUnmarshal(data, &task1)
	assert.Equal(t, task, task1)
}

func TestDatabaseFlushTask_Bytes(t *testing.T) {
	task := DatabaseFlushTask{
		DatabaseName: "test",
	}
	data := task.Bytes()
	task1 := DatabaseFlushTask{}
	_ = encoding.JSONUnmarshal(data, &task1)
	assert.Equal(t, task, task1)
}
