/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workqueue

import (
	"sync"
	"time"

	"k8s.io/utils/clock"
)

// The higher the priority value, the higher the priority
const (
	defaultMinPriority = 0
	defaultMaxPriority = 12
)

type GetPriorityFunc func(item interface{}) int

// NewPriority constructs a new work priority queue (see the package comment).
func NewPriority() *PriorityType {
	return NewNamedPriority("", defaultMaxPriority, func(item interface{}) int {
		return defaultMinPriority
	})
}

func NewNamedPriority(name string, maxPriority int, f GetPriorityFunc) *PriorityType {
	rc := clock.RealClock{}
	return newPriorityQueue(
		maxPriority,
		f,
		rc,
		globalMetricsFactory.newQueueMetrics(name, rc),
		defaultUnfinishedWorkUpdatePeriod,
	)
}

func newPriorityQueue(maxPriority int, f GetPriorityFunc, c clock.WithTicker, metrics queueMetrics, updatePeriod time.Duration) *PriorityType {
	t := &PriorityType{
		minPriority:                defaultMinPriority,
		maxPriority:                maxPriority,
		getPriorityFunc:            f,
		priorityQueue:              map[int][]t{},
		clock:                      c,
		dirty:                      set{},
		processing:                 set{},
		cond:                       sync.NewCond(&sync.Mutex{}),
		metrics:                    metrics,
		unfinishedWorkUpdatePeriod: updatePeriod,
	}

	// Don't start the goroutine for a type of noMetrics so we don't consume
	// resources unnecessarily
	if _, ok := metrics.(noMetrics); !ok {
		go t.updateUnfinishedWorkLoop()
	}

	return t
}

// PriorityType is a work queue (see the package comment).
type PriorityType struct {
	minPriority     int
	maxPriority     int
	getPriorityFunc GetPriorityFunc

	// queue defines the order in which we will work on items. Every
	// element of queue should be in the dirty set and not in the
	// processing set.
	priorityQueue map[int][]t

	// dirty defines all of the items that need to be processed.
	dirty set

	// Things that are currently being processed are in the processing set.
	// These things may be simultaneously in the dirty set. When we finish
	// processing something and remove it from this set, we'll check if
	// it's in the dirty set, and if so, add it to the queue.
	processing set

	cond *sync.Cond

	shuttingDown bool
	drain        bool

	metrics queueMetrics

	unfinishedWorkUpdatePeriod time.Duration
	clock                      clock.WithTicker
}

// Add marks item as needing processing.
func (q *PriorityType) Add(item interface{}) {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	if q.shuttingDown {
		return
	}
	if q.dirty.has(item) {
		return
	}

	q.metrics.add(item)

	q.dirty.insert(item)
	if q.processing.has(item) {
		return
	}

	priority := q.getPriorityFunc(item)
	priority = clipInt(priority, q.minPriority, q.maxPriority)
	q.priorityQueue[priority] = append(q.priorityQueue[priority], item)
	q.cond.Signal()
}

func clipInt(v, min, max int) int {
	switch {
	case v < min:
		return min
	case v > max:
		return max
	default:
		return v
	}
}

// Len returns the current queue length, for informational purposes only. You
// shouldn't e.g. gate a call to Add() or Get() on Len() being a particular
// value, that can't be synchronized properly.
func (q *PriorityType) Len() int {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()

	return q.lenNoLock()
}
func (q *PriorityType) lenNoLock() int {
	count := 0
	for i := q.minPriority; i <= q.maxPriority; i++ {
		count += len(q.priorityQueue[i])
	}
	return count
}

// Get blocks until it can return an item to be processed. If shutdown = true,
// the caller should end their goroutine. You must call Done with item when you
// have finished processing it.
func (q *PriorityType) Get() (item interface{}, shutdown bool) {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	for q.lenNoLock() == 0 && !q.shuttingDown {
		q.cond.Wait()
	}
	if q.lenNoLock() == 0 {
		// We must be shutting down.
		return nil, true
	}

	currentPriority := q.maxPriority + 1
	for i := q.maxPriority; i >= q.minPriority; i-- {
		if len(q.priorityQueue[i]) > 0 {
			currentPriority = i
			break
		}
	}

	item = q.priorityQueue[currentPriority][0]
	// The underlying array still exists and reference this object, so the object will not be garbage collected.
	q.priorityQueue[currentPriority][0] = nil
	q.priorityQueue[currentPriority] = q.priorityQueue[currentPriority][1:]

	q.metrics.get(item)

	q.processing.insert(item)
	q.dirty.delete(item)

	return item, false
}

// Done marks item as done processing, and if it has been marked as dirty again
// while it was being processed, it will be re-added to the queue for
// re-processing.
func (q *PriorityType) Done(item interface{}) {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()

	q.metrics.done(item)

	q.processing.delete(item)
	if q.dirty.has(item) {
		priority := q.getPriorityFunc(item)
		q.priorityQueue[priority] = append(q.priorityQueue[priority], item)
		q.cond.Signal()
	} else if q.processing.len() == 0 {
		q.cond.Signal()
	}
}

// ShutDown will cause q to ignore all new items added to it and
// immediately instruct the worker goroutines to exit.
func (q *PriorityType) ShutDown() {
	q.setDrain(false)
	q.shutdown()
}

// ShutDownWithDrain will cause q to ignore all new items added to it. As soon
// as the worker goroutines have "drained", i.e: finished processing and called
// Done on all existing items in the queue; they will be instructed to exit and
// ShutDownWithDrain will return. Hence: a strict requirement for using this is;
// your workers must ensure that Done is called on all items in the queue once
// the shut down has been initiated, if that is not the case: this will block
// indefinitely. It is, however, safe to call ShutDown after having called
// ShutDownWithDrain, as to force the queue shut down to terminate immediately
// without waiting for the drainage.
func (q *PriorityType) ShutDownWithDrain() {
	q.setDrain(true)
	q.shutdown()
	for q.isProcessing() && q.shouldDrain() {
		q.waitForProcessing()
	}
}

// isProcessing indicates if there are still items on the work queue being
// processed. It's used to drain the work queue on an eventual shutdown.
func (q *PriorityType) isProcessing() bool {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	return q.processing.len() != 0
}

// waitForProcessing waits for the worker goroutines to finish processing items
// and call Done on them.
func (q *PriorityType) waitForProcessing() {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	// Ensure that we do not wait on a queue which is already empty, as that
	// could result in waiting for Done to be called on items in an empty queue
	// which has already been shut down, which will result in waiting
	// indefinitely.
	if q.processing.len() == 0 {
		return
	}
	q.cond.Wait()
}

func (q *PriorityType) setDrain(shouldDrain bool) {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	q.drain = shouldDrain
}

func (q *PriorityType) shouldDrain() bool {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	return q.drain
}

func (q *PriorityType) shutdown() {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()
	q.shuttingDown = true
	q.cond.Broadcast()
}

func (q *PriorityType) ShuttingDown() bool {
	q.cond.L.Lock()
	defer q.cond.L.Unlock()

	return q.shuttingDown
}

func (q *PriorityType) updateUnfinishedWorkLoop() {
	t := q.clock.NewTicker(q.unfinishedWorkUpdatePeriod)
	defer t.Stop()
	for range t.C() {
		if !func() bool {
			q.cond.L.Lock()
			defer q.cond.L.Unlock()
			if !q.shuttingDown {
				q.metrics.updateUnfinishedWork()
				return true
			}
			return false

		}() {
			return
		}
	}
}
