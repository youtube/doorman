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

	pb "doorman/proto/doorman"
)

const (
	fiveMinutes = time.Duration(5) * time.Minute
	fiveSeconds = time.Duration(5) * time.Second
)

func makeResourceTemplate(name string, algo pb.Algorithm_Kind) *pb.ResourceTemplate {
	return &pb.ResourceTemplate{
		IdentifierGlob: string(name),
		Capacity:       float64(100),
		Algorithm: &pb.Algorithm{
			Kind:                 algo,
			RefreshInterval:      int64(5),
			LeaseLength:          int64(20),
			LearningModeDuration: int64(0),
		},
	}
}

func TestGlobMatches(t *testing.T) {
	// FIXME(ryszard): It seems this test is slightly flaky.
	s, err := MakeTestServer(makeResourceTemplate("*", pb.Algorithm_FAIR_SHARE))
	if err != nil {
		t.Errorf("MakeTestServer: %v", err)
	}

	res := s.getOrCreateResource("foo")
	if res.ID != "foo" {
		t.Errorf("res.ID = %v, want %v (%+v)", res.ID, "foo", res)
	}

	res2 := s.getOrCreateResource("foo")
	t.Logf("res1: %#v %p, res2: %#v %p", res, res, res2, res2)
	t.Logf("res1.store: %#v %p res2.store: %#v %p", res.store, res.store, res2.store, res2.store)
	if res2 != res {
		t.Errorf("s.getOrCreateResource: second call didn't get the same value: %#v != %#v", res2, res)
	}

}

func TestFullMatch(t *testing.T) {
	s, err := MakeTestServer(makeResourceTemplate("foo", pb.Algorithm_FAIR_SHARE),
		makeResourceTemplate("*", pb.Algorithm_FAIR_SHARE))
	if err != nil {
		t.Errorf("MakeTestServer: %v", err)
	}

	res := s.getOrCreateResource("foo")
	if got, want := res.config.IdentifierGlob, "foo"; got != want {
		t.Errorf(`res.config.GetIdentifierGlob() = %v, want %v`, got, want)
	}
}

func TestDecide(t *testing.T) {
	for algo := range algorithms {
		server, err := MakeTestServer(
			makeResourceTemplate("resource", algo),
			makeResourceTemplate("*", pb.Algorithm_NO_ALGORITHM))
		if err != nil {
			t.Errorf("MakeTestServer: %v", err)
		}

		resource := server.getOrCreateResource("resource")
		resource.Decide(Request{
			Client:     "new_client",
			Has:        0.0,
			Wants:      10.0,
			Subclients: 1,
		})
	}
}

func testSafeCapacity(t *testing.T) {
	template := makeResourceTemplate("res_with_safe_caoacity", pb.Algorithm_FAIR_SHARE)
	template.SafeCapacity = float64(10.0)
	s, err := MakeTestServer(template,
		makeResourceTemplate("*", pb.Algorithm_FAIR_SHARE))
	if err != nil {
		t.Errorf("MakeTestServer: %v", err)
	}

	res := s.getOrCreateResource("res_with_safe_capacity")
	res.store.Assign("client1", fiveMinutes, fiveSeconds, 10, 100, 1)
	res.store.Assign("client2", fiveMinutes, fiveSeconds, 10, 100, 1)
	resp := &pb.ResourceResponse{}
	res.SetSafeCapacity(resp)

	if resp.SafeCapacity != 10 {
		t.Errorf("*resp.SafeCapacity: want:10, got:%v", resp.SafeCapacity)
	}

	res = s.getOrCreateResource("res_without_safe_capacity")
	res.store.Assign("client1", fiveMinutes, fiveSeconds, 10, 100, 1)
	res.store.Assign("client2", fiveMinutes, fiveSeconds, 10, 100, 1)
	resp = &pb.ResourceResponse{}
	res.SetSafeCapacity(resp)

	if resp.SafeCapacity != 50 {
		t.Errorf("*resp.SafeCapacity: want:50, got:%v", resp.SafeCapacity)
	}
}

func TestStatusDoesNotCauseDeadlock(t *testing.T) {
	s, err := MakeTestServer(makeResourceTemplate("*", pb.Algorithm_FAIR_SHARE))
	if err != nil {
		t.Errorf("MakeTestServer: %v", err)
	}

	res := s.getOrCreateResource("foo")
	if res.ID != "foo" {
		t.Errorf("res.ID = %v, want %v (%+v)", res.ID, "foo", res)
	}

	returnC := make(chan bool)
	go func() {
		res.Status()
		returnC <- true
	}()

	// Setting a timer helps us to avoid waiting for test's timeout
	// in case the test is broken.
	select {
	case <-returnC:
	case <-time.After(5 * time.Second):
		t.Errorf("res.Status() caused deadlock")
	}
}
