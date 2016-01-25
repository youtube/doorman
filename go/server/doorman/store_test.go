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
	"testing"
	"time"
)

func TestMapStore(t *testing.T) {
	store := NewLeaseStore("test")

	store.Assign("a", 10*time.Second, 1*time.Second, 10.0, 12.0, 1)
	store.Assign("b", 10*time.Second, 1*time.Second, 10.0, 12.0, 1)
	store.Assign("c", 20*time.Second, 1*time.Second, 15.0, 20.0, 1)

	if want, got := 35.0, store.SumHas(); want != got {
		t.Errorf("store.SumHas() = %v, want %v", got, want)
	}

	if want, got := 44.0, store.SumWants(); want != got {
		t.Errorf("store.SumWants() = %v, want %v", got, want)
	}

	if want, got := 10.0, store.Get("a").Has; want != got {
		t.Errorf(`store.Get("a") = %v, want %v`, got, want)
	}

	if want, got := int64(3), store.Count(); want != got {
		t.Errorf("store.Count() = %v, want %v", got, want)
	}

	time.Sleep(10 * time.Second)
	store.Clean()

	if want, got := 15.0, store.SumHas(); want != got {
		t.Errorf("store.SumHas() = %v, got %v", want, got)
	}

	if want, got := 20.0, store.SumWants(); want != got {
		t.Errorf("store.SumWants() = %v, got %v", want, got)
	}

	if !store.Get("a").IsZero() {
		t.Errorf("lease for client a is present: %v", store.Get("a"))
	}

	store.Release("c")

	if got := store.Get("c"); !got.IsZero() {
		t.Errorf("lease for client c is present: %v", got)
	}

	if got, want := store.SumWants(), 0.0; got != want {
		t.Errorf("store.SumWants() = %v, want %v", got, want)
	}

	if got, want := store.SumHas(), 0.0; got != want {
		t.Errorf("store.SumHas() = %v, want %v", got, want)
	}

	if want, got := int64(0), store.Count(); want != got {
		t.Errorf("store.Count() = %v, want %v", got, want)
	}
}
