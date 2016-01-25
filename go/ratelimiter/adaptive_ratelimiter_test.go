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
	"testing"
	"time"

	"golang.org/x/net/context"
)

func TestAdaptiveWait(t *testing.T) {
	res := &fakeResource{
		id:       "qps_shard1",
		wants:    500.0,
		capacity: make(chan float64),
	}

	arl := NewAdaptiveQPS(res)
	defer arl.Close()

	// Unblock the rate limiter.
	res.capacity <- 10

	// Create context with timeout so that we do not have to wait
	// if the test is broken.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := arl.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClearOldEvents(t *testing.T) {
	e := &entries{
		times: make([]time.Time, 0),
	}

	// Set window to a very small duration so the test could
	// could pass quickly.
	window := 1 * time.Millisecond

	for i := 0; i < 20; i++ {
		e.Record(time.Now())
	}

	// Sleep for specified amount of time so all recorded
	// events become old.
	time.Sleep(2 * time.Millisecond)

	// Record a new event, which is supposed to stay in entries
	// after calling Clear.
	e.Record(time.Now())

	e.Clear(window)
	if got, want := len(e.times), 1; got != want {
		t.Errorf("entries length got %v, want %v", got, want)
	}
}

func TestGetWants(t *testing.T) {
	// epsilon is used in comparison of floats.
	const epsilon = 0.0000000001
	numEntries := 9
	e := &entries{
		times: make([]time.Time, 0),
	}

	// Set window to a very small duration so the test
	// could pass quickly.
	window := 1 * time.Second

	for i := 0; i < numEntries; i++ {
		e.Record(time.Now())
	}

	wants := float64(numEntries) * window.Seconds() / float64(numEntries*(numEntries+1)/2)
	if got, want := e.GetWants(window), wants; math.Abs(got-want) > epsilon {
		t.Errorf("Wants value got %v, want %v", got, want)
	}
}
