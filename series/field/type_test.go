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

package field

import (
	"testing"

	"github.com/lindb/lindb/aggregation/function"

	"github.com/stretchr/testify/assert"
)

func TestDownSamplingFunc(t *testing.T) {
	assert.Equal(t, function.Sum, SumField.DownSamplingFunc())
	assert.Equal(t, function.Min, MinField.DownSamplingFunc())
	assert.Equal(t, function.Max, MaxField.DownSamplingFunc())
	assert.Equal(t, function.LastValue, GaugeField.DownSamplingFunc())
	assert.Equal(t, function.Unknown, Unknown.DownSamplingFunc())
}

func TestType_String(t *testing.T) {
	assert.Equal(t, "sum", SumField.String())
	assert.Equal(t, "max", MaxField.String())
	assert.Equal(t, "min", MinField.String())
	assert.Equal(t, "gauge", GaugeField.String())
	assert.Equal(t, "unknown", Unknown.String())
}

func TestIsSupportFunc(t *testing.T) {
	assert.True(t, SumField.IsFuncSupported(function.Sum))
	assert.True(t, SumField.IsFuncSupported(function.Min))
	assert.True(t, SumField.IsFuncSupported(function.Max))
	assert.False(t, SumField.IsFuncSupported(function.Quantile))

	assert.True(t, MaxField.IsFuncSupported(function.Max))
	assert.False(t, MaxField.IsFuncSupported(function.Quantile))

	assert.True(t, GaugeField.IsFuncSupported(function.LastValue))
	assert.False(t, GaugeField.IsFuncSupported(function.Quantile))

	assert.True(t, MinField.IsFuncSupported(function.Min))
	assert.False(t, MinField.IsFuncSupported(function.Quantile))

	assert.False(t, Unknown.IsFuncSupported(function.Quantile))
}

func TestType_GetAggFunc(t *testing.T) {
	assert.Equal(t, maxAggregator, MaxField.GetAggFunc())
	assert.Equal(t, sumAggregator, SumField.GetAggFunc())
	assert.Equal(t, minAggregator, MinField.GetAggFunc())
	assert.Equal(t, maxAggregator, Unknown.GetAggFunc())
}
