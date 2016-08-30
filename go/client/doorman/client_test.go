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
	"net"
	"runtime"
	"testing"
	"time"

	"golang.org/x/net/context"

	log "github.com/golang/glog"
	rpc "google.golang.org/grpc"

	server "doorman/go/server/doorman"

	pb "doorman/proto/doorman"
)

var (

	// rpcTimeout is used for RPCs to the doorman server.
	rpcTimeout = 2 * time.Second
)

type fixture struct {
	server    *server.Server
	rpcServer *rpc.Server
	lis       net.Listener
}

func (fix *fixture) TearDown() {
	if fix.lis != nil {
		fix.lis.Close()
	}
	if fix.rpcServer != nil {
		fix.rpcServer.Stop()
	}

}

func (fix *fixture) Addr() string {
	return fix.lis.Addr().String()
}

func setUp() (*fixture, error) {
	var (
		fix fixture
		err error
	)

	fix.server, err = server.MakeTestServer(&pb.ResourceTemplate{
		IdentifierGlob: string("*"),
		Capacity:       float64(100),
		SafeCapacity:   float64(2),
		Algorithm: &pb.Algorithm{
			Kind:                 pb.Algorithm_FAIR_SHARE,
			RefreshInterval:      int64(5),
			LeaseLength:          int64(20),
			LearningModeDuration: int64(60),
		},
	})
	if err != nil {
		return nil, err
	}
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		return &fixture{}, err
	}

	fix.lis = lis
	fix.rpcServer = rpc.NewServer()
	pb.RegisterCapacityServer(fix.rpcServer, fix.server)

	go fix.rpcServer.Serve(lis)

	return &fix, nil
}

func TestOnlyOneResource(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Fatal(err)
	}
	defer fix.TearDown()

	client, err := NewWithID(fix.lis.Addr().String(), "test_client",
		DialOpts(rpc.WithInsecure(), rpc.WithTimeout(rpcTimeout)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = client.Resource("resource", 10.0); err != nil {
		t.Fatal(err)
	}

	if _, err := client.Resource("resource", 10.0); err != ErrDuplicateResourceID {
		t.Errorf("expected ErrDuplicateResourceID, got %v", err)
	}

}

type nonMasterServer struct {
	pb.CapacityServer
	master string
}

func (server nonMasterServer) GetCapacity(_ context.Context, _ *pb.GetCapacityRequest) (out *pb.GetCapacityResponse, err error) {
	out = new(pb.GetCapacityResponse)
	out.Mastership = &pb.Mastership{
		MasterAddress: string(server.master),
	}
	return out, nil
}

func receiveWithTimeout(t *testing.T, channel chan float64, length time.Duration) {
	timeout := time.NewTimer(length)
	select {
	case <-channel:
	case <-timeout.C:
		t.Errorf("timed out")
	}

}

func TestMastershipReconnect(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Fatal(err)
	}
	defer fix.TearDown()

	nonMaster := nonMasterServer{master: fix.lis.Addr().String()}

	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()

	rpcServer := rpc.NewServer()
	pb.RegisterCapacityServer(rpcServer, nonMaster)

	go rpcServer.Serve(lis)
	defer rpcServer.Stop()

	client, err := NewWithID(lis.Addr().String(), "test_client",
		DialOpts(rpc.WithInsecure(), rpc.WithTimeout(rpcTimeout)))
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.Resource("resource", 10.0)
	if err != nil {
		log.Error(err)
	}
	receiveWithTimeout(t, res.Capacity(), 1*time.Second)
}

func TestPriority(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Fatal(err)
	}
	defer fix.TearDown()

	client, err := NewWithID(fix.Addr(), "test_client",
		DialOpts(rpc.WithInsecure(), rpc.WithTimeout(rpcTimeout)))
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.ResourceWithPriority("resource", 10.0, 20)
	if err != nil {
		log.Error(err)
	}
	receiveWithTimeout(t, res.Capacity(), 1*time.Second)
}

func waitUntilChannelClosed(res Resource) {
	for {
		runtime.Gosched()

		select {
		case _, ok := <-res.Capacity():
			if !ok {
				return
			}
		}
	}
}

func TestRelease(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Fatal(err)
	}
	defer fix.TearDown()

	client, err := NewWithID(fix.Addr(), "test_client",
		DialOpts(rpc.WithInsecure(), rpc.WithTimeout(rpcTimeout)))
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.Resource("resource", 10.0)
	if err != nil {
		log.Error(err)
	}

	err = res.Release()
	if err != nil {
		log.Error(err)
	}

	// After releasing the resource the associated channel should eventually
	// be closed.
	waitUntilChannelClosed(res)

	// Releasing the resource again should be just fine.
	err = res.Release()
	if err != nil {
		log.Error(err)
	}
}

func TestCloseClient(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Fatal(err)
	}
	defer fix.TearDown()

	client, err := NewWithID(fix.Addr(), "test_client",
		DialOpts(rpc.WithInsecure(), rpc.WithTimeout(rpcTimeout)))
	if err != nil {
		t.Fatal(err)
	}

	res1, err := client.Resource("resource1", 10.0)
	if err != nil {
		log.Error(err)
	}

	res2, err := client.Resource("resource2", 10.0)
	if err != nil {
		log.Error(err)
	}

	client.Close()

	// After closing the client the associated resource channels
	// should eventually be closed.
	waitUntilChannelClosed(res1)
	waitUntilChannelClosed(res2)
}
