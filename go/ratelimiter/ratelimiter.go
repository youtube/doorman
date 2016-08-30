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
	"math"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"doorman/go/client/doorman"
)

// RateLimiter is a rate limiter that works with Doorman resources.
type RateLimiter interface {
	// Wait blocks until the appropriate operation runs or an error occurs.
	Wait(ctx context.Context) error

	// Close closes the rate limiter.
	Close()
}

// qpsRateLimiter is the implementation of rate limiter interface
// for QPS as the resource.
type qpsRateLimiter struct {
	// resource that this rate limiter is limiting.
	resource doorman.Resource

	// quit is used to notify that rate limiter is to be closed.
	quit chan bool

	// events is used by Wait to request a channel it should be waiting on from run.
	// This will be a channel on which receive will succeed immediately, if the rate
	// limiter is unlimited, or the main unfreeze channel otherwise.
	events chan chan chan bool

	// interval indicates period of time once per which the
	// rate limiter releases goroutines waiting for the access to the resource.
	interval time.Duration

	// rate is a limit at which rate limiter releases waiting goroutines
	// that want to access the resource.
	rate int

	// subintervals is the number of subintervals.
	subintervals int
}

// NewQPS creates a rate limiter connected to the resourse.
func NewQPS(res doorman.Resource) RateLimiter {
	rl := &qpsRateLimiter{
		resource: res,
		quit:     make(chan bool),
		events:   make(chan chan chan bool),
	}

	go rl.run()
	return rl
}

// Close closes the rate limiter. It panics if called more than once.
func (rl *qpsRateLimiter) Close() {
	rl.quit <- true
}

// recalculate calculates new values for rate limit and interval.
func (rl *qpsRateLimiter) recalculate(rate int, interval int) (newRate int, leftoverRate int, newInterval time.Duration) {
	newRate = rate
	newInterval = time.Duration(interval) * time.Millisecond

	// If the rate limit is more than 2 Hz we are going to divide the given
	// interval to some number of subintervals and distribute the given rate
	// among these subintervals to avoid burstiness which could take place otherwise.
	if rate > 1 && interval >= 20 {
		// Try to have one event per subinterval, but don't let subintervals go
		// below 20ms, that pounds on things too hard.
		rl.subintervals = int(math.Min(float64(rate), float64(interval/20)))

		newRate = rate / rl.subintervals
		leftoverRate = rate % rl.subintervals

		interval = int(float64(newRate*interval) / float64(rate))
		newInterval = time.Duration(interval) * time.Millisecond
	}
	return
}

// update sets rate and interval to be appropriate for the capacity received.
func (rl *qpsRateLimiter) update(capacity float64) (leftoverRate int) {
	switch {
	case capacity < 0:
		rl.rate = -1
	case capacity == 0:
		// We block rate limiter, meaning no access to resource at all.
		rl.rate = 0
	case capacity <= 10:
		rl.rate, leftoverRate, rl.interval = rl.recalculate(1, int(1000.0/capacity))
	default:
		rl.rate, leftoverRate, rl.interval = rl.recalculate(int(capacity), 1000)
	}
	return
}

func (rl *qpsRateLimiter) unlimited() bool {
	return rl.rate < 0
}

func (rl *qpsRateLimiter) blocked() bool {
	return rl.rate == 0
}

// run is the rate limiter's main loop. It takes care of receiving
// a new capacity for the resource and releasing goroutines waiting
// for the access to the resource at the calculated rate.
func (rl *qpsRateLimiter) run() {
	var (
		// unfreeze is used to notify waiting goroutines that they can access the resource.
		// run will attempt to send values on unfreeze at the available rate.
		unfreeze = make(chan bool)

		// released reflects number of times we released waiting goroutines per original
		// interval.
		released = 0

		// leftoverRate is a rate that left after dividing the original rate by number of subintervals.
		// We need to redistribute it among subintervals so we release waiting goroutines at exactly
		// original rate.
		leftoverRate, leftoverRateOriginal = 0, 0
	)

	for {
		var wakeUp <-chan time.Time

		// We only need to wake up if the rate limiter is neither blocked
		// nor unlimited. If it is blocked, there is nothing to wake up for.
		// When it is unlimited, we will be sending a non-blocking channel
		// to the waiting goroutine anyway.
		if !rl.blocked() && !rl.unlimited() {
			wakeUp = time.After(rl.interval)
		}

		select {
		case <-rl.quit:
			// Notify closing goroutine that we're done so it
			// could safely close another channels.
			close(unfreeze)
			return
		case capacity := <-rl.resource.Capacity():
			// Updates rate and interval according to received capacity value.
			leftoverRateOriginal = rl.update(capacity)

			// Set released to 0, as a new cycle of goroutines' releasing begins.
			released = 0
			leftoverRate = leftoverRateOriginal
		case response := <-rl.events:
			// If the rate limiter is unlimited, we send back a channel on which
			// we will immediately send something, unblocking the call to Wait
			// that it sent there.
			if rl.unlimited() {
				nonBlocking := make(chan bool)
				response <- nonBlocking
				nonBlocking <- true
				break
			}
			response <- unfreeze
		case <-wakeUp:
			// Release waiting goroutines when timer is expired.
			max := rl.rate
			if released < rl.subintervals {
				if leftoverRate > 0 {
					stepLeftoverRate := leftoverRate/rl.rate + 1
					max += stepLeftoverRate
					leftoverRate -= stepLeftoverRate
				}
				released++
			} else {
				// Set released to 0, as a new cycle of goroutines releasing begins.
				released = 0
				leftoverRate = leftoverRateOriginal
			}

			for i := 0; i < max; i++ {
				select {
				case unfreeze <- true:
					// We managed to release a goroutine
					// waiting on the other side of the channel.
				default:
					// We failed to send value through channel, so nobody
					// is waiting for the resource now, but we keep trying
					// sending values via channel, because a waiting goroutine
					// could eventually appear before rate is reached
					// and we have to release it.
				}
			}
		}
	}
}

// Wait blocks until a time appropriate operation to run or an error occurs.
func (rl *qpsRateLimiter) Wait(ctx context.Context) error {
	response := make(chan chan bool)
	rl.events <- response
	unfreeze := <-response

	select {
	case <-ctx.Done():
		return ctx.Err()
	case _, ok := <-unfreeze:
		if !ok {
			return grpc.Errorf(codes.ResourceExhausted, "rate limiter was closed")
		}
		return nil
	}
}
