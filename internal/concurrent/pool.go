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

package concurrent

import (
	"context"
	"sync"
	"time"

	"github.com/lindb/lindb/internal/linmetric"

	"go.uber.org/atomic"
)

const (
	// size of the queue that workers register their availability to the dispatcher.
	readyWorkerQueueSize = 32
	// size of the tasks queue
	tasksCapacity = 8
	// sleeps in this interval when there are no available workers
	sleepInterval = time.Millisecond * 5
)

// Task represents a task function to be executed by a worker(goroutine).
type Task func()

// Pool represents the goroutine pool that executes submitted tasks.
type Pool interface {
	// Submit enqueues a callable task for a worker to execute.
	//
	// Each submitted task is immediately given to an ready worker.
	// If there are no available workers, the dispatcher starts a new worker,
	// until the maximum number of workers are added.
	//
	// After the maximum number of workers are running, and no workers are ready,
	// execute function will be blocked.
	Submit(task Task)
	// SubmitAndWait executes the task and waits for it to be executed.
	SubmitAndWait(task Task)
	// Stopped returns true if this pool has been stopped.
	Stopped() bool
	// Stop stops all goroutines gracefully,
	// all pending tasks will be finished before exit
	Stop()
}

// workerPool is a pool for goroutines.
type workerPool struct {
	name                string
	maxWorkers          int
	tasks               chan Task                    // tasks channel
	readyWorkers        chan *worker                 // available worker
	idleTimeout         time.Duration                // idle goroutine recycle time
	onDispatcherStopped chan struct{}                // signal that dispatcher is stopped
	stopped             atomic.Bool                  // mark if the pool is closed or not
	workersAlive        *linmetric.BoundGauge        // current workers count in use
	workersCreated      *linmetric.BoundDeltaCounter // workers created count since start
	workersKilled       *linmetric.BoundDeltaCounter // workers killed since start
	tasksConsumed       *linmetric.BoundDeltaCounter // tasks consumed count
	tasksWaitingTime    *linmetric.BoundDeltaCounter // tasks waiting total time
	tasksExecutingTime  *linmetric.BoundDeltaCounter // tasks executing total time with waiting period
	ctx                 context.Context
	cancel              context.CancelFunc
}

// NewPool returns a new worker pool,
// maxWorkers parameter specifies the maximum number workers that will execute tasks concurrently.
func NewPool(name string, maxWorkers int, idleTimeout time.Duration, scope linmetric.Scope) Pool {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool := &workerPool{
		name:                name,
		maxWorkers:          maxWorkers,
		tasks:               make(chan Task, tasksCapacity),
		readyWorkers:        make(chan *worker, readyWorkerQueueSize),
		idleTimeout:         idleTimeout,
		onDispatcherStopped: make(chan struct{}),
		stopped:             *atomic.NewBool(false),
		workersAlive:        scope.NewGauge("workers_alive"),
		workersCreated:      scope.NewDeltaCounter("workers_created"),
		workersKilled:       scope.NewDeltaCounter("workers_killed"),
		tasksConsumed:       scope.NewDeltaCounter("tasks_consumed"),
		tasksWaitingTime:    scope.NewDeltaCounter("tasks_waiting_duration_sum"),
		tasksExecutingTime:  scope.NewDeltaCounter("tasks_executing_duration_sum"),
		ctx:                 ctx,
		cancel:              cancel,
	}
	go pool.dispatch()
	return pool
}

func (p *workerPool) Submit(task Task) {
	if task == nil || p.Stopped() {
		return
	}
	startTime := time.Now()
	p.tasks <- func() {
		p.tasksWaitingTime.Add(float64(time.Since(startTime).Nanoseconds() / 1e6))
		task()
		p.tasksExecutingTime.Add(float64(time.Since(startTime).Nanoseconds() / 1e6))
	}
}

func (p *workerPool) SubmitAndWait(task Task) {
	if task == nil || p.Stopped() {
		return
	}
	startTime := time.Now()
	worker := p.mustGetWorker()
	p.tasksWaitingTime.Add(float64(time.Since(startTime).Nanoseconds() / 1e6))
	doneChan := make(chan struct{})
	worker.execute(func() {
		task()
		close(doneChan)
	})
	<-doneChan
	p.tasksExecutingTime.Add(float64(time.Since(startTime).Nanoseconds() / 1e6))
}

// mustGetWorker makes sure that a ready worker is return
func (p *workerPool) mustGetWorker() *worker {
	var worker *worker
	for {
		select {
		// got a worker
		case worker = <-p.readyWorkers:
			return worker
		default:
			if int(p.workersAlive.Get()) >= p.maxWorkers {
				// no available workers
				time.Sleep(sleepInterval)
				continue
			}
			w := newWorker(p)
			return w
		}
	}
}

func (p *workerPool) dispatch() {
	defer func() {
		p.onDispatcherStopped <- struct{}{}
	}()

	idleTimeoutTimer := time.NewTimer(p.idleTimeout)
	defer idleTimeoutTimer.Stop()
	var (
		worker *worker
		task   Task
	)

	for {
		idleTimeoutTimer.Reset(p.idleTimeout)
		select {
		case <-p.ctx.Done():
			return
		case task = <-p.tasks:
			worker := p.mustGetWorker()
			worker.execute(task)
		case <-idleTimeoutTimer.C:
			// timed out waiting, kill a ready worker
			if p.workersAlive.Get() > 0 {
				select {
				case worker = <-p.readyWorkers:
					worker.stop(func() {})
				default:
					// workers are busy now
				}
			}
		}
	}
}

func (p *workerPool) Stopped() bool {
	return p.stopped.Load()
}

// stopWorkers stops all workers
func (p *workerPool) stopWorkers() {
	var wg sync.WaitGroup
	for p.workersAlive.Get() > 0 {
		wg.Add(1)
		worker := <-p.readyWorkers
		worker.stop(func() {
			wg.Done()
		})
	}
	wg.Wait()
}

// consumedRemainingTasks consumes all buffered tasks in the channel
func (p *workerPool) consumedRemainingTasks() {
	for {
		select {
		case task := <-p.tasks:
			task()
			p.tasksConsumed.Incr()
		default:
			return
		}
	}
}

// Stop tells the dispatcher to exit with pending tasks done.
func (p *workerPool) Stop() {
	if p.stopped.Swap(true) {
		return
	}
	// close dispatcher
	p.cancel()
	// wait dispatcher's exit
	<-p.onDispatcherStopped
	// close all workers
	p.stopWorkers()
	// consume remaining tasks
	p.consumedRemainingTasks()
}

// worker represents the worker that executes the task
type worker struct {
	pool   *workerPool
	tasks  chan Task
	stopCh chan struct{}
}

// newWorker creates the worker that executes tasks given by the dispatcher
// When a new worker starts, it registers itself on the createdWorkers channel.
func newWorker(pool *workerPool) *worker {
	w := &worker{
		pool:   pool,
		tasks:  make(chan Task),
		stopCh: make(chan struct{}),
	}
	w.pool.workersAlive.Incr()
	w.pool.workersCreated.Incr()
	go w.process()
	return w
}

// execute submits the task to queue
func (w *worker) execute(task Task) {
	w.tasks <- task
}

func (w *worker) stop(callable func()) {
	defer callable()
	w.stopCh <- struct{}{}
	w.pool.workersKilled.Incr()
	w.pool.workersAlive.Decr()
}

// process process task from queue
func (w *worker) process() {
	var task Task
	for {
		select {
		case <-w.stopCh:
			return
		case task = <-w.tasks:
			task()
			w.pool.tasksConsumed.Incr()
			// register worker-self to readyWorkers again
			w.pool.readyWorkers <- w
		}
	}
}
