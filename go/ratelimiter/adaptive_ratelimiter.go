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
	"sort"
	"time"

	log "github.com/golang/glog"
	"doorman/go/client/doorman"
	"golang.org/x/net/context"
)

// AdaptiveQPS is a rate limiter that will try to guess what is the
// optional capacity to request. It will try to fo as fast as possible,
// but it will release some capacity if the client cannot keep up with
// the capacity it received.
type AdaptiveQPS struct {
	ratelimiter RateLimiter
	resource    doorman.Resource
	entry       chan time.Time
	quit        chan bool
	opts        *adaptiveOptions
}

// NewAdaptiveQPS creates a rate limiter connected to the resource.
func NewAdaptiveQPS(res doorman.Resource, options ...AdaptiveOption) RateLimiter {
	arl := &AdaptiveQPS{
		ratelimiter: NewQPS(res),
		resource:    res,
		entry:       make(chan time.Time),
		quit:        make(chan bool),
		opts:        getAdaptiveOptions(options),
	}

	go arl.run()
	return arl
}

// run takes care of receiving new duration record from waiting goroutines.
func (arl *AdaptiveQPS) run() {
	// entries is used to record entry times to Wait method.
	e := &entries{
		times: make([]time.Time, 0),
	}

	for {
		select {
		case <-arl.quit:
			// Stop receiving entry time records and exit.
			close(arl.entry)
			return
		case entry := <-arl.entry:
			// Record a new entry to Wait method.
			e.Record(entry)
		case <-time.After(arl.opts.window):
			// Recalculate wants capacity and ask for its updated value.
			wants := e.GetWants(arl.opts.window)
			if err := arl.resource.Ask(wants); err != nil {
				log.Errorf("resource.Ask: %v", err)
			}
		}
	}

}

// adaptiveOptions are options for adaptive rate limiter.
type adaptiveOptions struct {
	// window is the duration over which we calculate
	// the desired capacity (wants).
	window time.Duration
}

// AdaptiveOption configures an adaptive rate limiter.
type AdaptiveOption func(*adaptiveOptions)

// Window configures how often statistics about capacity usage are collected.
// A shorter value gives you a quicker reaction time, but at the cost of increasing
// the risk of oscillations. The default value is 10 seconds.
func Window(w time.Duration) AdaptiveOption {
	return func(opts *adaptiveOptions) {
		opts.window = w
	}
}

func getAdaptiveOptions(options []AdaptiveOption) *adaptiveOptions {
	opts := &adaptiveOptions{
		window: 10 * time.Second,
	}

	for _, opt := range options {
		opt(opts)
	}

	return opts
}

// entries is used to record entry times to rate limiter's Wait method.
type entries struct {
	times []time.Time
}

// Record records a new entry to rate limiter's Wait method.
func (e *entries) Record(entry time.Time) {
	e.times = append(e.times, entry)
}

// Clear removes old events: ones which happened more than specified
// window ago.
func (e *entries) Clear(window time.Duration) {
	i := sort.Search(len(e.times), func(i int) bool {
		return time.Since(e.times[i]) < window
	})
	e.times = e.times[i:]
}

// GetWants calculates wants capacity based on number of entries recorded
// during "window" duration.
func (e *entries) GetWants(window time.Duration) float64 {
	// Get rid of old events.
	e.Clear(window)

	// frequency keeps information about how many events happened
	// in a particular second (within "window" last seconds).
	frequency := make(map[int]int)

	// Calculate number of events per every second.
	for _, entry := range e.times {
		frequency[int(time.Since(entry).Seconds())]++
	}

	// Calculate the following sum: for every second within window
	// we multiply the number of events that occured in this particular
	// second by the second's weight. The weight given to a second is
	// proportional to its recency (with interval of 10 seconds, the most
	// recent second will have a weight of 10, while the 10th second will
	// have the weigth of 1).
	var sum int
	for i, n := 0, int(window.Seconds()); i < n; i++ {
		sum += frequency[i] * (n - i)
	}

	return float64(sum) / float64(len(e.times)*(len(e.times)+1)/2)
}

// Wait records entry time and blocks until the goroutine is released.
func (arl *AdaptiveQPS) Wait(ctx context.Context) error {
	// TODO: if entry is closed and we try to send
	// a value via it, it will cause panic.
	//
	// Do not block.
	go func() {
		arl.entry <- time.Now()
	}()

	if err := arl.ratelimiter.Wait(ctx); err != nil {
		return err
	}

	return nil
}

// Close closes the adjustible rate limiter. It should be called only once.
func (arl *AdaptiveQPS) Close() {
	arl.quit <- true
	arl.ratelimiter.Close()
}
