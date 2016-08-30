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

package doorman

import (
	"math"
	"testing"
	"time"

	pb "doorman/proto/doorman"
)

type testCase struct {
	client     string
	has        float64
	wants      float64
	shouldGet  float64
	subclients int64
}

func testAlgorithm(t *testing.T, cases []testCase, capacity float64, algoFunc func(*pb.Algorithm) Algorithm, respectMax, preloadStore bool) LeaseStore {
	store, algo := NewLeaseStore("test"), algoFunc(new(pb.Algorithm))

	if preloadStore {
		leaseLength, _ := time.ParseDuration("300s")
		refreshInterval, _ := time.ParseDuration("5s")

		for _, c := range cases {
			store.Assign(c.client, leaseLength, refreshInterval, c.has, c.wants, c.subclients)
		}
	}

	for i, c := range cases {
		if lease := algo(store, capacity, Request{
			Client:     c.client,
			Has:        c.has,
			Wants:      c.wants,
			Subclients: c.subclients,
		}); lease.Has != c.shouldGet {
			t.Errorf("lease.Has = %v (for %v), want %v (case %v)", lease.Has, c.client, c.shouldGet, i+1)
		}

		if respectMax && store.SumHas() > capacity {
			t.Errorf("store.SumHas() > store.Capacity() (%v > %v)", store.SumHas(), capacity)
		}
	}

	return store
}

func TestNoAlgorithm(t *testing.T) {
	store := testAlgorithm(t, []testCase{
		{
			client:     "a",
			wants:      10,
			shouldGet:  10,
			subclients: 1,
		},
		{
			client:     "b",
			wants:      100,
			shouldGet:  100,
			subclients: 1,
		},
	}, 0, NoAlgorithm, false, false)

	if got := store.SumHas(); got != 110 {
		t.Errorf("store.SumHas() = %v, want 110", got)
	}

}

func TestStatic(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "a",
			wants:      100,
			shouldGet:  100,
			subclients: 1,
		},
		{
			client:     "b",
			wants:      10,
			shouldGet:  10,
			subclients: 1,
		},
		{
			client:     "c",
			wants:      120,
			shouldGet:  100,
			subclients: 1,
		},
	}, 100, Static, false, false)
}

func TestFairShare(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      1000,
			shouldGet:  55,
			subclients: 1,
		},
		{
			client:     "c1",
			wants:      60,
			shouldGet:  55,
			subclients: 1,
		},
		{
			client:     "c2",
			wants:      10,
			shouldGet:  10,
			subclients: 1,
		},
	}, 120, FairShare, true, true)
}

func TestFairShareLowerExtra(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      1000,
			shouldGet:  60,
			subclients: 1,
		},
		{
			client:     "c1",
			wants:      50,
			shouldGet:  50,
			subclients: 1,
		},
		{
			client:     "c2",
			wants:      10,
			shouldGet:  10,
			subclients: 1,
		},
	}, 120, FairShare, true, true)

}

func TestFairShareWithMultipleSubclients(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      1000,
			shouldGet:  60,
			subclients: 6,
		},
		{
			client:     "c1",
			wants:      500,
			shouldGet:  40,
			subclients: 4,
		},
		{
			client:     "c2",
			wants:      200,
			shouldGet:  20,
			subclients: 2,
		},
	}, 120, FairShare, true, true)
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      2000,
			shouldGet:  200,
			subclients: 10,
		},
		{
			client:     "c1",
			wants:      500,
			shouldGet:  200,
			subclients: 10,
		},
		{
			client:     "c2",
			wants:      700,
			shouldGet:  600,
			subclients: 30,
		},
	}, 1000, FairShare, true, true)
}

func TestProportionalShare(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      60,
			shouldGet:  55,
			subclients: 1,
		},
		{
			client:     "c1",
			wants:      60,
			shouldGet:  55,
			subclients: 1,
		},
		{
			client:     "c2",
			wants:      10,
			shouldGet:  10,
			subclients: 1,
		},
	}, 120, ProportionalShare, true, true)
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      60,
			shouldGet:  60,
			subclients: 1,
		},
		{
			client:     "c1",
			wants:      75,
			shouldGet:  60,
			subclients: 1,
		},
		{
			client:     "c2",
			wants:      10,
			shouldGet:  0,
			subclients: 1,
		},
	}, 120, ProportionalShare, true, false)
}

func TestProportionalShareWithMultipleSubclients(t *testing.T) {
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      65,
			shouldGet:  60,
			subclients: 3,
		},
		{
			client:     "c1",
			wants:      45,
			shouldGet:  40,
			subclients: 2,
		},
		{
			client:     "c2",
			wants:      20,
			shouldGet:  20,
			subclients: 1,
		},
	}, 120, ProportionalShare, true, true)
	testAlgorithm(t, []testCase{
		{
			client:     "c0",
			wants:      65,
			shouldGet:  65,
			subclients: 3,
		},
		{
			client:     "c1",
			wants:      45,
			shouldGet:  45,
			subclients: 2,
		},
		{
			client:     "c2",
			wants:      20,
			shouldGet:  10,
			subclients: 1,
		},
	}, 120, ProportionalShare, true, false)
}

func TestLeaseLengthAndRefreshInterval(t *testing.T) {
	const (
		leaseLength     = 342
		refreshInterval = 5
	)

	store, algo := NewLeaseStore("test"), ProportionalShare(&pb.Algorithm{
		LeaseLength:     int64(leaseLength),
		RefreshInterval: int64(refreshInterval),
	})

	now := time.Now()
	lease := algo(store, 100, Request{
		Client:     "b",
		Wants:      10,
		Subclients: 1,
	})

	leaseLengthSec := lease.Expiry.Unix() - now.Unix()

	if math.Abs(float64(leaseLengthSec-leaseLength)) > 1 {
		t.Errorf("lease.Expiry = %v (%d seconds), want %d seconds", lease.Expiry, leaseLengthSec, leaseLength)
	}

	if lease.RefreshInterval.Seconds() != refreshInterval {
		t.Errorf("lease.RefreshInterval = %v, want %d seconds", lease.RefreshInterval, refreshInterval)
	}
}
