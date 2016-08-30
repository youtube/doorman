// Copyright 2016 Google, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ratelimiter

import (
	"errors"
	"testing"
	"time"

	"doorman/go/client/doorman"
	"golang.org/x/net/context"
)

type fakeResource struct {
	id       string
	capacity chan float64
	wants    float64
}

// Implements basic Resource interface, without mutex protection of fields.
func (r *fakeResource) Capacity() chan float64 {
	return r.capacity
}

func (r *fakeResource) Ask(wants float64) error {
	if wants <= 0 {
		return errors.New("wants must be > 0.0")
	}

	r.wants = wants
	return nil
}

// Fake implementation of resource Release().
func (r *fakeResource) Release() error {
	return nil
}

func (r *fakeResource) Wants() float64 {
	return r.wants
}

func TestWaitWithCanceledContext(t *testing.T) {
	// Resource instance to work with for RateLimiter.
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500,
		capacity: make(chan float64),
	}

	rl := NewQPS(res)
	defer rl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, want := rl.Wait(ctx), context.Canceled; got != want {
		t.Errorf("rl.Wait(_) = %v; want %v", got, want)
	}
}

func TestBlockedRateLimiterBlocks(t *testing.T) {
	// Resource instance to work with for RateLimiter.
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500,
		capacity: make(chan float64),
	}

	rl := NewQPS(res)
	defer rl.Close()

	// Send 0 to rate limiter, which means blocking the access
	// to resource completely.
	res.capacity <- 0

	waitC := make(chan error)
	go func() {
		waitC <- rl.Wait(context.Background())
	}()

	select {
	case <-waitC:
		t.Errorf("rl.Wait should not have returned")
	case <-time.After(50 * time.Millisecond):
	}

	// Now set capacity so it could be released.
	res.capacity <- 1
	if err := <-waitC; err != nil {
		t.Errorf("rl.Wait(_) = %v; want nil", err)
	}
}

func TestLimitedRateMakesWait(t *testing.T) {
	// Resource instance to work with for RateLimiter.
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500,
		capacity: make(chan float64),
	}

	rl := NewQPS(res)
	defer rl.Close()

	res.capacity <- 10

	// Create context with timeout so that we do not have to wait
	// if the test is broken.
	ctx, _ := context.WithTimeout(context.Background(), 200*time.Millisecond)
	start := time.Now()
	// As we set capacity to 10, timer expiration will happen every 1 second.
	// So Wait() shall return within 100 millisecond.
	if err := rl.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	if got, want := time.Since(start)/time.Millisecond, 100*time.Microsecond; got > want {
		t.Errorf("Duration got %v; want %v", got, want)
	}
}

func BenchmarkInfiniteRateDoesNotBlock(b *testing.B) {
	// Resource instance to work with for RateLimiter.
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500,
		capacity: make(chan float64),
	}

	rl := NewQPS(res)
	defer rl.Close()

	ctx := context.Background()
	res.Capacity() <- -1

	for i := 0; i < b.N; i++ {
		err := rl.Wait(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func singleThreadedWait(capacity float64, rl RateLimiter, res doorman.Resource, numLoops int) (time.Duration, error) {
	ctx := context.Background()
	res.Capacity() <- capacity

	start := time.Now()
	for i := 0; i < numLoops; i++ {
		err := rl.Wait(ctx)
		if err != nil {
			return 0, err
		}
	}

	return time.Since(start), nil
}

func TestInfiniteRateDoesNotBlock(t *testing.T) {
	// Resource instance to work with for RateLimiter.
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500,
		capacity: make(chan float64),
	}

	rl := NewQPS(res)
	defer rl.Close()

	duration, err := singleThreadedWait(-1, rl, res, 500)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := duration/time.Second, time.Duration(0); got > want {
		t.Errorf("Duration got %v; want %v", got, want)
	}
}
