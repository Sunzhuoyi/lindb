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

package aggregation

import (
	"github.com/lindb/lindb/pkg/timeutil"
	"github.com/lindb/lindb/series"
)

//go:generate mockgen -source=./group_agg.go -destination=./group_agg_mock.go -package=aggregation

// GroupingAggregator represents an aggregator which merges time series and does grouping if need
type GroupingAggregator interface {
	// Aggregate aggregates the time series data
	Aggregate(it series.GroupedIterator)
	// ResultSet returns the result set of aggregator
	ResultSet() []series.GroupedIterator
}

type groupingAggregator struct {
	aggSpecs   AggregatorSpecs
	interval   timeutil.Interval
	timeRange  timeutil.TimeRange
	aggregates map[string]series.GroupedIterator // tag values => field aggregates
}

// NewGroupingAggregator creates a grouping aggregator
func NewGroupingAggregator(
	interval timeutil.Interval,
	timeRange timeutil.TimeRange,
	aggSpecs AggregatorSpecs,
) GroupingAggregator {
	return &groupingAggregator{
		aggSpecs:   aggSpecs,
		interval:   interval,
		timeRange:  timeRange,
		aggregates: make(map[string]series.GroupedIterator),
	}
}

// Aggregate aggregates the time series data
func (ga *groupingAggregator) Aggregate(it series.GroupedIterator) {
	ga.aggregates[it.Tags()] = it
	//tags := it.Tags()
	//seriesAgg := ga.getAggregator(tags)
	//var sAgg SeriesAggregator
	//for it.HasNext() {
	//	seriesIt := it.Next()
	//	fieldName := seriesIt.FieldName()
	//	fieldType := seriesIt.FieldType()
	//	// 1. find field aggregator
	//	sAgg = nil
	//	for _, aggregator := range seriesAgg {
	//		if aggregator.FieldName() == fieldName {
	//			sAgg = aggregator
	//			break
	//		}
	//	}
	//	if sAgg == nil {
	//		continue
	//	}
	//	// set field type for aggregate
	//	sAgg.SetFieldType(fieldType)
	//	// 2. merge the field series data
	//	for seriesIt.HasNext() {
	//		startTime, fieldIt := seriesIt.Next()
	//		if fieldIt == nil {
	//			continue
	//		}
	//		_, _ = sAgg.GetAggregateBlock(startTime)
	//		fmt.Println("dlkfjaslkdf")
	//		//TODO impl
	//		//if ok {
	//		//fAgg.Aggregate(fieldIt)
	//		//}
	//	}
	//}
}

// ResultSet returns the result set of aggregator
func (ga *groupingAggregator) ResultSet() []series.GroupedIterator {
	length := len(ga.aggregates)
	if length == 0 {
		return nil
	}
	seriesList := make([]series.GroupedIterator, length)
	idx := 0
	for _, aggregator := range ga.aggregates {
		seriesList[idx] = aggregator
		//.ResultSet()
		idx++
	}
	return seriesList
}

//// getAggregator returns the time series aggregator by time series's tags
//func (ga *groupingAggregator) getAggregator(tags string) (agg FieldAggregates) {
//	// 2. get series aggregator
//	agg, ok := ga.aggregates[tags]
//	if !ok {
//		agg = NewFieldAggregates(ga.interval, 1, ga.timeRange, false, ga.aggSpecs)
//		ga.aggregates[tags] = agg
//	}
//	return
//}
