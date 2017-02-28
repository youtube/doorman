// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"golang.org/x/net/context"
)

// TestElectionWait tests if followers can correctly wait for elections.
func TestElectionWait(t *testing.T) {
	clus := NewClusterV3(t, &ClusterConfig{Size: 3})
	defer clus.Terminate(t)
	defer dropSessionLease(clus)

	leaders := 3
	followers := 3
	var clients []*clientv3.Client
	newClient := makeMultiNodeClients(t, clus.cluster, &clients)

	electedc := make(chan string)
	nextc := []chan struct{}{}

	// wait for all elections
	donec := make(chan struct{})
	for i := 0; i < followers; i++ {
		nextc = append(nextc, make(chan struct{}))
		go func(ch chan struct{}) {
			for j := 0; j < leaders; j++ {
				b := concurrency.NewElection(newClient(), "test-election")
				cctx, cancel := context.WithCancel(context.TODO())
				defer cancel()
				s, ok := <-b.Observe(cctx)
				if !ok {
					t.Fatalf("could not observe election; channel closed")
				}
				electedc <- string(s.Kvs[0].Value)
				// wait for next election round
				<-ch
			}
			donec <- struct{}{}
		}(nextc[i])
	}

	// elect some leaders
	for i := 0; i < leaders; i++ {
		go func() {
			e := concurrency.NewElection(newClient(), "test-election")
			ev := fmt.Sprintf("electval-%v", time.Now().UnixNano())
			if err := e.Campaign(context.TODO(), ev); err != nil {
				t.Fatalf("failed volunteer (%v)", err)
			}
			// wait for followers to accept leadership
			for j := 0; j < followers; j++ {
				s := <-electedc
				if s != ev {
					t.Errorf("wrong election value got %s, wanted %s", s, ev)
				}
			}
			// let next leader take over
			if err := e.Resign(context.TODO()); err != nil {
				t.Fatalf("failed resign (%v)", err)
			}
			// tell followers to start listening for next leader
			for j := 0; j < followers; j++ {
				nextc[j] <- struct{}{}
			}
		}()
	}

	// wait on followers
	for i := 0; i < followers; i++ {
		<-donec
	}

	closeClients(t, clients)
}

// TestElectionFailover tests that an election will
func TestElectionFailover(t *testing.T) {
	clus := NewClusterV3(t, &ClusterConfig{Size: 3})
	defer clus.Terminate(t)
	defer dropSessionLease(clus)

	cctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// first leader (elected)
	e := concurrency.NewElection(clus.clients[0], "test-election")
	if err := e.Campaign(context.TODO(), "foo"); err != nil {
		t.Fatalf("failed volunteer (%v)", err)
	}

	// check first leader
	resp, ok := <-e.Observe(cctx)
	if !ok {
		t.Fatalf("could not wait for first election; channel closed")
	}
	s := string(resp.Kvs[0].Value)
	if s != "foo" {
		t.Fatalf("wrong election result. got %s, wanted foo", s)
	}

	// next leader
	electedc := make(chan struct{})
	go func() {
		ee := concurrency.NewElection(clus.clients[1], "test-election")
		if eer := ee.Campaign(context.TODO(), "bar"); eer != nil {
			t.Fatal(eer)
		}
		electedc <- struct{}{}
	}()

	// invoke leader failover
	session, serr := concurrency.NewSession(clus.clients[0])
	if serr != nil {
		t.Fatal(serr)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	// check new leader
	e = concurrency.NewElection(clus.clients[2], "test-election")
	resp, ok = <-e.Observe(cctx)
	if !ok {
		t.Fatalf("could not wait for second election; channel closed")
	}
	s = string(resp.Kvs[0].Value)
	if s != "bar" {
		t.Fatalf("wrong election result. got %s, wanted bar", s)
	}

	// leader must ack election (otherwise, Campaign may see closed conn)
	<-electedc
}

// TestElectionSessionRelock ensures that campaigning twice on the same election
// with the same lock will Proclaim instead of deadlocking.
func TestElectionSessionRecampaign(t *testing.T) {
	clus := NewClusterV3(t, &ClusterConfig{Size: 1})
	defer clus.Terminate(t)
	cli := clus.RandClient()

	e := concurrency.NewElection(cli, "test-elect")
	if err := e.Campaign(context.TODO(), "abc"); err != nil {
		t.Fatal(err)
	}
	e2 := concurrency.NewElection(cli, "test-elect")
	if err := e2.Campaign(context.TODO(), "def"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	if resp := <-e.Observe(ctx); len(resp.Kvs) == 0 || string(resp.Kvs[0].Value) != "def" {
		t.Fatalf("expected value=%q, got response %v", "def", resp)
	}
}
