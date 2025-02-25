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

package memdb

import (
	"io"
	"sync"

	"github.com/lindb/roaring"
	"go.uber.org/atomic"

	"github.com/lindb/lindb/flow"
	"github.com/lindb/lindb/internal/linmetric"
	"github.com/lindb/lindb/pkg/logger"
	"github.com/lindb/lindb/pkg/timeutil"
	protoMetricsV1 "github.com/lindb/lindb/proto/gen/v1/metrics"
	"github.com/lindb/lindb/series/field"
	"github.com/lindb/lindb/tsdb/tblstore/metricsdata"
)

//go:generate mockgen -source ./database.go -destination=./database_mock.go -package memdb

var memDBLogger = logger.GetLogger("tsdb", "MemDB")

var (
	memDBScope               = linmetric.NewScope("lindb.tsdb.memdb")
	pageAllocatedCounterVec  = memDBScope.NewDeltaCounterVec("allocated_pages", "db")
	pageAllocatedFailuresVec = memDBScope.NewDeltaCounterVec("allocated_page_failures", "db")
)

// MemoryDatabase is a database-like concept of Shard as memTable in cassandra.
type MemoryDatabase interface {
	// AcquireWrite acquires writing data points
	AcquireWrite()
	// Write writes metrics to the memory-database,
	// return error on exceeding max count of tagsIdentifier or writing failure
	Write(point *MetricPoint) error
	// WithLock retrieves the lock of memdb, and returns the release function
	WithLock() (release func())
	// WriteWithoutLock must be called after WithLock
	// Used for batch write
	WriteWithoutLock(point *MetricPoint) error
	// CompleteWrite completes writing data points
	CompleteWrite()
	// FlushFamilyTo flushes the corresponded family data to builder.
	// Close is not in the flushing process.
	FlushFamilyTo(flusher metricsdata.Flusher) error
	// MemSize returns the memory-size of this metric-store
	MemSize() int32
	// DataFilter filters the data based on condition
	flow.DataFilter
	// Closer closes the memory database resource
	io.Closer
}

type memoryDBMetrics struct {
	allocatedPages        *linmetric.BoundDeltaCounter
	allocatedPageFailures *linmetric.BoundDeltaCounter
}

func newMemoryDBMetrics(name string) *memoryDBMetrics {
	return &memoryDBMetrics{
		allocatedPages:        pageAllocatedCounterVec.WithTagValues(name),
		allocatedPageFailures: pageAllocatedFailuresVec.WithTagValues(name),
	}
}

// MemoryDatabaseCfg represents the memory database config
type MemoryDatabaseCfg struct {
	FamilyTime int64
	Name       string
	TempPath   string
}

// flushContext holds the context for flushing
type flushContext struct {
	metricID uint32

	timeutil.SlotRange // start/end time slot, metric level flush context
}

// memoryDatabase implements MemoryDatabase.
type memoryDatabase struct {
	familyTime int64
	name       string

	mStores *MetricBucketStore // metric id => mStoreINTF
	buf     DataPointBuffer

	writeCondition sync.WaitGroup
	rwMutex        sync.RWMutex // lock of create metric store

	allocSize atomic.Int32 // allocated size
	metrics   memoryDBMetrics
}

// NewMemoryDatabase returns a new MemoryDatabase.
func NewMemoryDatabase(cfg MemoryDatabaseCfg) (MemoryDatabase, error) {
	buf, err := newDataPointBuffer(cfg.TempPath)
	if err != nil {
		return nil, err
	}
	return &memoryDatabase{
		familyTime: cfg.FamilyTime,
		name:       cfg.Name,
		buf:        buf,
		mStores:    NewMetricBucketStore(),
		allocSize:  *atomic.NewInt32(0),
		metrics:    *newMemoryDBMetrics(cfg.Name),
	}, err
}

// getOrCreateMStore returns the mStore by metricHash.
func (md *memoryDatabase) getOrCreateMStore(metricID uint32) (mStore mStoreINTF) {
	mStore, ok := md.mStores.Get(metricID)
	if !ok {
		// not found need create new metric store
		mStore = newMetricStore()
		md.allocSize.Add(emptyMStoreSize)
		md.mStores.Put(metricID, mStore)
	}
	// found metric store in current memory database
	return
}

// AcquireWrite acquires writing data points
func (md *memoryDatabase) AcquireWrite() {
	md.writeCondition.Add(1)
}

// CompleteWrite completes writing data points
func (md *memoryDatabase) CompleteWrite() {
	md.writeCondition.Done()
}

type MetricPoint struct {
	MetricID  uint32
	SeriesID  uint32
	SlotIndex uint16
	FieldIDs  []field.ID
	Proto     *protoMetricsV1.Metric
}

func (md *memoryDatabase) WithLock() (release func()) {
	md.rwMutex.Lock()
	return md.rwMutex.Unlock
}

func (md *memoryDatabase) Write(point *MetricPoint) error {
	md.rwMutex.Lock()
	defer md.rwMutex.Unlock()
	return md.WriteWithoutLock(point)
}

func (md *memoryDatabase) WriteWithoutLock(point *MetricPoint) error {
	mStore := md.getOrCreateMStore(point.MetricID)
	tStore, size := mStore.GetOrCreateTStore(point.SeriesID)
	written := false
	var fieldIDIdx = 0
	afterWrite := func(writtenLinFieldSize int) {
		fieldIDIdx++
		size += writtenLinFieldSize
		written = true
	}

	simpleFields := point.Proto.SimpleFields
	for simpleFieldIdx := range simpleFields {
		var (
			fieldType field.Type
		)
		switch point.Proto.SimpleFields[simpleFieldIdx].Type {
		case protoMetricsV1.SimpleFieldType_DELTA_SUM, protoMetricsV1.SimpleFieldType_CUMULATIVE_SUM:
			fieldType = field.SumField
		case protoMetricsV1.SimpleFieldType_GAUGE:
			fieldType = field.GaugeField
		default:
			continue
		}
		writtenLinFieldSize, err := md.writeLinField(
			point.SlotIndex,
			point.FieldIDs[fieldIDIdx], fieldType, simpleFields[simpleFieldIdx].Value,
			mStore, tStore,
		)
		if err != nil {
			return err
		}
		afterWrite(writtenLinFieldSize)
	}
	compoundField := point.Proto.CompoundField

	var (
		err                 error
		writtenLinFieldSize int
	)
	if compoundField == nil {
		goto End
	}

	// write histogram_min
	if compoundField.Min > 0 {
		writtenLinFieldSize, err := md.writeLinField(
			point.SlotIndex, point.FieldIDs[fieldIDIdx],
			field.MinField, compoundField.Min,
			mStore, tStore)
		if err != nil {
			return err
		}
		afterWrite(writtenLinFieldSize)
	}
	// write histogram_max
	if compoundField.Max > 0 {
		writtenLinFieldSize, err := md.writeLinField(
			point.SlotIndex, point.FieldIDs[fieldIDIdx],
			field.MinField, compoundField.Max,
			mStore, tStore)
		if err != nil {
			return err
		}
		afterWrite(writtenLinFieldSize)
	}
	// write histogram_sum
	writtenLinFieldSize, err = md.writeLinField(
		point.SlotIndex, point.FieldIDs[fieldIDIdx],
		field.SumField, compoundField.Sum,
		mStore, tStore)
	if err != nil {
		return err
	}
	afterWrite(writtenLinFieldSize)

	// write histogram_count
	writtenLinFieldSize, err = md.writeLinField(
		point.SlotIndex, point.FieldIDs[fieldIDIdx],
		field.SumField, compoundField.Count,
		mStore, tStore)
	if err != nil {
		return err
	}
	afterWrite(writtenLinFieldSize)

	// write __bucket_${boundary}
	// assume that length of ExplicitBounds equals to Values
	// data must be valid before write
	for idx := range compoundField.ExplicitBounds {
		writtenLinFieldSize, err = md.writeLinField(
			point.SlotIndex, point.FieldIDs[fieldIDIdx],
			field.HistogramField, compoundField.Values[idx],
			mStore, tStore)
		if err != nil {
			return err
		}
		afterWrite(writtenLinFieldSize)
	}

End:
	if written {
		mStore.SetSlot(point.SlotIndex)
	}
	md.allocSize.Add(int32(size))
	return nil
}

func (md *memoryDatabase) writeLinField(
	slotIndex uint16,
	fieldID field.ID, fieldType field.Type, fieldValue float64,
	mStore mStoreINTF, tStore tStoreINTF,
) (writtenSize int, err error) {
	fStore, ok := tStore.GetFStore(fieldID)
	if !ok {
		buf, err := md.buf.AllocPage()
		if err != nil {
			md.metrics.allocatedPageFailures.Incr()
			return 0, err
		}
		md.metrics.allocatedPages.Incr()
		fStore = newFieldStore(buf, fieldID)
		writtenSize += tStore.InsertFStore(fStore)
		// if write data success, add field into metric level for cache
		mStore.AddField(fieldID, fieldType)
	}
	writtenSize += fStore.Write(fieldType, slotIndex, fieldValue)
	return writtenSize, nil
}

// FlushFamilyTo flushes all data related to the family from metric-stores to builder.
func (md *memoryDatabase) FlushFamilyTo(flusher metricsdata.Flusher) error {
	// waiting current writing complete
	md.writeCondition.Wait()

	if err := md.mStores.WalkEntry(func(key uint32, value mStoreINTF) error {
		if err := value.FlushMetricsDataTo(flusher, flushContext{
			metricID: key,
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return flusher.Commit()
}

// Filter filters the data based on metric/seriesIDs,
// if finds data then returns the flow.FilterResultSet, else returns nil
func (md *memoryDatabase) Filter(metricID uint32,
	seriesIDs *roaring.Bitmap,
	timeRange timeutil.TimeRange,
	fields field.Metas,
) ([]flow.FilterResultSet, error) {
	md.rwMutex.RLock()
	defer md.rwMutex.RUnlock()

	mStore, ok := md.mStores.Get(metricID)
	if !ok {
		return nil, nil
	}
	//TODO filter slot range
	return mStore.Filter(md.familyTime, seriesIDs, fields)
}

// MemSize returns the time series database memory size
func (md *memoryDatabase) MemSize() int32 {
	return md.allocSize.Load()
}

// Close closes memory data point buffer
func (md *memoryDatabase) Close() error {
	return md.buf.Close()
}
