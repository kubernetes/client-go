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

package workqueue_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
)

func TestBasic(t *testing.T) {
	tests := []struct {
		queue         *workqueue.Type
		queueShutDown func(workqueue.Interface)
	}{
		{
			queue:         workqueue.New(),
			queueShutDown: workqueue.Interface.ShutDown,
		},
		{
			queue:         workqueue.New(),
			queueShutDown: workqueue.Interface.ShutDownWithDrain,
		},
	}
	for _, test := range tests {
		// If something is seriously wrong this test will never complete.

		// Start producers
		const producers = 50
		producerWG := sync.WaitGroup{}
		producerWG.Add(producers)
		for i := 0; i < producers; i++ {
			go func(i int) {
				defer producerWG.Done()
				for j := 0; j < 50; j++ {
					test.queue.Add(i)
					time.Sleep(time.Millisecond)
				}
			}(i)
		}

		// Start consumers
		const consumers = 10
		consumerWG := sync.WaitGroup{}
		consumerWG.Add(consumers)
		for i := 0; i < consumers; i++ {
			go func(i int) {
				defer consumerWG.Done()
				for {
					item, quit := test.queue.Get()
					if item == "added after shutdown!" {
						t.Errorf("Got an item added after shutdown.")
					}
					if quit {
						return
					}
					t.Logf("Worker %v: begin processing %v", i, item)
					time.Sleep(3 * time.Millisecond)
					t.Logf("Worker %v: done processing %v", i, item)
					test.queue.Done(item)
				}
			}(i)
		}

		producerWG.Wait()
		test.queueShutDown(test.queue)
		test.queue.Add("added after shutdown!")
		consumerWG.Wait()
		if test.queue.Len() != 0 {
			t.Errorf("Expected the queue to be empty, had: %v items", test.queue.Len())
		}
	}
}

func TestAddWhileProcessing(t *testing.T) {
	tests := []struct {
		queue         *workqueue.Type
		queueShutDown func(workqueue.Interface)
	}{
		{
			queue:         workqueue.New(),
			queueShutDown: workqueue.Interface.ShutDown,
		},
		{
			queue:         workqueue.New(),
			queueShutDown: workqueue.Interface.ShutDownWithDrain,
		},
	}
	for _, test := range tests {

		// Start producers
		const producers = 50
		producerWG := sync.WaitGroup{}
		producerWG.Add(producers)
		for i := 0; i < producers; i++ {
			go func(i int) {
				defer producerWG.Done()
				test.queue.Add(i)
			}(i)
		}

		// Start consumers
		const consumers = 10
		consumerWG := sync.WaitGroup{}
		consumerWG.Add(consumers)
		for i := 0; i < consumers; i++ {
			go func(i int) {
				defer consumerWG.Done()
				// Every worker will re-add every item up to two times.
				// This tests the dirty-while-processing case.
				counters := map[interface{}]int{}
				for {
					item, quit := test.queue.Get()
					if quit {
						return
					}
					counters[item]++
					if counters[item] < 2 {
						test.queue.Add(item)
					}
					test.queue.Done(item)
				}
			}(i)
		}

		producerWG.Wait()
		test.queueShutDown(test.queue)
		consumerWG.Wait()
		if test.queue.Len() != 0 {
			t.Errorf("Expected the queue to be empty, had: %v items", test.queue.Len())
		}
	}
}

func TestAdd(t *testing.T) {

	q := workqueue.New()

	var added bool
	var shutdown bool

	firstItem := "foo"
	secondItem := "bar"

	t.Run("Add new item into the queue", func(t *testing.T) {
		added, shutdown = q.Add(firstItem)
		assert.True(t, added)
		assert.False(t, shutdown)
	})

	t.Run("Add an existing item into the queue", func(t *testing.T) {
		added, shutdown = q.Add(firstItem)
		assert.False(t, added)
		assert.False(t, shutdown)
	})

	finishedWG := sync.WaitGroup{}
	finishedWG.Add(1)
	go func() {
		defer finishedWG.Done()
		q.ShutDownWithDrain()
	}()

	// This is done as to simulate a sequence of events where ShutDownWithDrain
	// is called before we start marking all items as done - thus simulating a
	// drain where we wait for all items to finish processing.
	shuttingDown := false
	for !shuttingDown {
		_, shuttingDown = q.Get()
	}

	t.Run("Add an  and item when queue is in shutdown mode", func(t *testing.T) {
		added, shutdown = q.Add(secondItem)
		assert.False(t, added)
		assert.True(t, shutdown)
	})

	// Mark the firstItem as done, as to finish up
	q.Done(firstItem)

	finishedWG.Wait()
}

func TestLen(t *testing.T) {
	q := workqueue.New()
	q.Add("foo")
	if e, a := 1, q.Len(); e != a {
		t.Errorf("Expected %v, got %v", e, a)
	}
	q.Add("bar")
	if e, a := 2, q.Len(); e != a {
		t.Errorf("Expected %v, got %v", e, a)
	}
	q.Add("foo") // should not increase the queue length.
	if e, a := 2, q.Len(); e != a {
		t.Errorf("Expected %v, got %v", e, a)
	}
}

func TestReinsert(t *testing.T) {
	q := workqueue.New()
	q.Add("foo")

	// Start processing
	i, _ := q.Get()
	if i != "foo" {
		t.Errorf("Expected %v, got %v", "foo", i)
	}

	// Add it back while processing
	q.Add(i)

	// Finish it up
	q.Done(i)

	// It should be back on the queue
	i, _ = q.Get()
	if i != "foo" {
		t.Errorf("Expected %v, got %v", "foo", i)
	}

	// Finish that one up
	q.Done(i)

	if a := q.Len(); a != 0 {
		t.Errorf("Expected queue to be empty. Has %v items", a)
	}
}

func TestQueueDrainageUsingShutDownWithDrain(t *testing.T) {

	q := workqueue.New()

	q.Add("foo")
	q.Add("bar")

	firstItem, _ := q.Get()
	secondItem, _ := q.Get()

	finishedWG := sync.WaitGroup{}
	finishedWG.Add(1)
	go func() {
		defer finishedWG.Done()
		q.ShutDownWithDrain()
	}()

	// This is done as to simulate a sequence of events where ShutDownWithDrain
	// is called before we start marking all items as done - thus simulating a
	// drain where we wait for all items to finish processing.
	shuttingDown := false
	for !shuttingDown {
		_, shuttingDown = q.Get()
	}

	// Mark the first two items as done, as to finish up
	q.Done(firstItem)
	q.Done(secondItem)

	finishedWG.Wait()
}

func TestNoQueueDrainageUsingShutDown(t *testing.T) {

	q := workqueue.New()

	q.Add("foo")
	q.Add("bar")

	q.Get()
	q.Get()

	finishedWG := sync.WaitGroup{}
	finishedWG.Add(1)
	go func() {
		defer finishedWG.Done()
		// Invoke ShutDown: suspending the execution immediately.
		q.ShutDown()
	}()

	// We can now do this and not have the test timeout because we didn't call
	// Done on the first two items before arriving here.
	finishedWG.Wait()
}

func TestForceQueueShutdownUsingShutDown(t *testing.T) {

	q := workqueue.New()

	q.Add("foo")
	q.Add("bar")

	q.Get()
	q.Get()

	finishedWG := sync.WaitGroup{}
	finishedWG.Add(1)
	go func() {
		defer finishedWG.Done()
		q.ShutDownWithDrain()
	}()

	// This is done as to simulate a sequence of events where ShutDownWithDrain
	// is called before ShutDown
	shuttingDown := false
	for !shuttingDown {
		_, shuttingDown = q.Get()
	}

	// Use ShutDown to force the queue to shut down (simulating a caller
	// which can invoke this function on a second SIGTERM/SIGINT)
	q.ShutDown()

	// We can now do this and not have the test timeout because we didn't call
	// done on any of the items before arriving here.
	finishedWG.Wait()
}

func TestQueueDrainageUsingShutDownWithDrainWithDirtyItem(t *testing.T) {
	q := workqueue.New()

	q.Add("foo")
	gotten, _ := q.Get()
	q.Add("foo")

	finishedWG := sync.WaitGroup{}
	finishedWG.Add(1)
	go func() {
		defer finishedWG.Done()
		q.ShutDownWithDrain()
	}()

	// Ensure that ShutDownWithDrain has started and is blocked.
	shuttingDown := false
	for !shuttingDown {
		_, shuttingDown = q.Get()
	}

	// Finish "working".
	q.Done(gotten)

	// `shuttingDown` becomes false because Done caused an item to go back into
	// the queue.
	again, shuttingDown := q.Get()
	if shuttingDown {
		t.Fatalf("should not have been done")
	}
	q.Done(again)

	// Now we are really done.
	_, shuttingDown = q.Get()
	if !shuttingDown {
		t.Fatalf("should have been done")
	}

	finishedWG.Wait()
}

// TestGarbageCollection ensures that objects that are added then removed from the queue are
// able to be garbage collected.
func TestGarbageCollection(t *testing.T) {
	type bigObject struct {
		data []byte
	}
	leakQueue := workqueue.New()
	t.Cleanup(func() {
		// Make sure leakQueue doesn't go out of scope too early
		runtime.KeepAlive(leakQueue)
	})
	c := &bigObject{data: []byte("hello")}
	mustGarbageCollect(t, c)
	leakQueue.Add(c)
	o, _ := leakQueue.Get()
	leakQueue.Done(o)
}

// mustGarbageCollect asserts than an object was garbage collected by the end of the test.
// The input must be a pointer to an object.
func mustGarbageCollect(t *testing.T, i interface{}) {
	t.Helper()
	var collected int32 = 0
	runtime.SetFinalizer(i, func(x interface{}) {
		atomic.StoreInt32(&collected, 1)
	})
	t.Cleanup(func() {
		if err := wait.PollImmediate(time.Millisecond*100, wait.ForeverTestTimeout, func() (done bool, err error) {
			// Trigger GC explicitly, otherwise we may need to wait a long time for it to run
			runtime.GC()
			return atomic.LoadInt32(&collected) == 1, nil
		}); err != nil {
			t.Errorf("object was not garbage collected")
		}
	})
}
