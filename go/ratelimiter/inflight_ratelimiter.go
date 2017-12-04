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
	"context"
	"github.com/youtube/doorman/go/client/doorman"
	"math"
	"sync"
	"time"
)

// MUST be ware that when new capacity is smaller than old capacity,
// in flight requests may be bigger than new capacity
type inFlightRateLimiter struct {
	// guard access to capacity, inflights and waiting
	sync.Mutex
	// resource that this rate limiter is limiting.
	resource doorman.Resource
	//
	capacity int
	// count of in flight request
	inflights int
	// count of blocking clients
	waiting int
	// when there are free resources, notify blocking clients
	semaphore chan bool
	// quit is used to notify that rate limiter is to be closed.
	quit chan bool
}

var _ RateLimiter = &inFlightRateLimiter{}

func NewInFlightRateLimiter(res doorman.Resource) RateLimiter {
	irl := &inFlightRateLimiter{
		semaphore: make(chan bool),
		quit:      make(chan bool),
	}
	go irl.run()
	return irl
}

// run is the rate limiter's main loop.
func (flight *inFlightRateLimiter) run() {
	// to wake up waiting clients periodically
	go func() {
		ticker:=time.NewTicker(time.Second)
		for _ =range ticker.C{
			select {
			case flight.semaphore <- true:
			default:
			}
		}
	}()

	for true {
		if capacity, ok := <-flight.resource.Capacity(); ok {
			flight.updateCapacity(capacity)
		} else {
			break
		}

	}
}

// // Wait blocks until a time appropriate operation to run or an error occurs.
func (flight *inFlightRateLimiter) Wait(ctx context.Context) error {
	var flag int
	flight.Lock()
	// have to lock this operation
	for flight.inflights >= flight.capacity {
		if flag == 0 {
			flight.waiting++
			flag++
		}

		flight.Unlock()
		// we have to release lock before this operation
		select {
		case <-ctx.Done():
			break
		case <-flight.semaphore:
		}
		flight.Lock()
	}
	flight.Unlock()

	flight.Lock()
	defer flight.Unlock()

	if ctx.Err() == nil {
		flight.inflights++
	}

	if flag != 0 {
		flight.waiting--
	}

	return nil
}

func (flight *inFlightRateLimiter) Return(ctx context.Context) error {
	flight.Lock()
	flight.inflights--
	flight.Unlock()

	select {
	case flight.semaphore <- true:
	default:
	}

	return nil
}

func (flight *inFlightRateLimiter) Close() {
	flight.quit <- true
}

func (flight *inFlightRateLimiter) updateCapacity(capacity int) {
	flight.Lock()
	old := flight.capacity
	flight.capacity = capacity

	if capacity > old && flight.waiting != 0 {
		for fresh := int(math.Min(capacity-old, flight.waiting)); fresh != 0; fresh-- {
			select {
			case flight.semaphore <- true:
			default:
			}
		}
	}
	flight.Unlock()
}
