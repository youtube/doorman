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
	"testing"
	"time"

	"golang.org/x/net/context"
	rpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "doorman/proto/doorman"
)

func TestValidateGetCapacityRequest(t *testing.T) {
	invalid := []*pb.GetCapacityRequest{
		// No ClientId.
		{
			ClientId: string(""),
			Resource: nil,
		},
		// No ResourceId.
		{
			ClientId: string("client"),
			Resource: []*pb.ResourceRequest{
				{
					ResourceId: string(""),
					Priority:   int64(1),
					Has:        new(pb.Lease),
					Wants:      float64(1),
				},
			},
		},
		// Requests negative capacity.
		{
			ClientId: string("client"),
			Resource: []*pb.ResourceRequest{
				{
					ResourceId: string("resource"),
					Priority:   int64(1),
					Has:        new(pb.Lease),
					Wants:      float64(-10),
				},
			},
		},
	}

	for _, p := range invalid {
		if err := validateGetCapacityRequest(p); err == nil {
			t.Errorf("no validation error raised for invalid %v", p)
		}
	}
}

func makeRepositories(groups ...[]*pb.ResourceTemplate) []*pb.ResourceRepository {
	repos := make([]*pb.ResourceRepository, len(groups))
	for i, group := range groups {
		repos[i] = &pb.ResourceRepository{Resources: group}
	}
	return repos

}

func TestValidateResourceRepository(t *testing.T) {
	invalid := makeRepositories(
		[]*pb.ResourceTemplate{}, // empty repo
		[]*pb.ResourceTemplate{ // no *
			{
				IdentifierGlob: string("foo"),
				Capacity:       float64(10.0),
			},
		},
		[]*pb.ResourceTemplate{ // no algorithm for *
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(10.0),
			},
		},
		[]*pb.ResourceTemplate{ // * is not the last template
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(10.0),
				Algorithm: &pb.Algorithm{
					Kind: pb.Algorithm_PROPORTIONAL_SHARE,
				},
			},
			{
				IdentifierGlob: string("foo"),
				Capacity:       float64(10.0),
			},
		},
		[]*pb.ResourceTemplate{ // malformed glob
			{
				IdentifierGlob: string("[*-]"),
				Capacity:       float64(10.0),
			},
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(10.0),
				Algorithm: &pb.Algorithm{
					Kind: pb.Algorithm_PROPORTIONAL_SHARE,
				},
			},
		},
	)

	for _, c := range invalid {
		if err := validateResourceRepository(c); err == nil {
			t.Errorf("validateResourceRepository(%+v): expected error", c)
		}
	}
}

type fixture struct {
	client    pb.CapacityClient
	server    *Server
	rpcServer *rpc.Server
	lis       net.Listener
}

func (fix fixture) tearDown() {
	if fix.rpcServer != nil {
		fix.rpcServer.Stop()
	}
	if fix.server != nil {
		fix.server.Close()
	}
	if fix.lis != nil {
		fix.lis.Close()
	}
}

func (fix fixture) Addr() string {
	return fix.lis.Addr().String()
}

// setUp sets up a test root server.
func setUp() (fixture, error) {
	return setUpIntermediate("test", "")
}

// setUpIntermediate sets up a test intermediate server.
func setUpIntermediate(name string, addr string) (fixture, error) {
	var (
		fix fixture
		err error
	)

	fix.server, err = MakeTestIntermediateServer(
		name, addr,
		&pb.ResourceTemplate{
			IdentifierGlob: string("*"),
			Capacity:       float64(100),
			SafeCapacity:   float64(2),
			Algorithm: &pb.Algorithm{
				Kind:            pb.Algorithm_PROPORTIONAL_SHARE,
				RefreshInterval: int64(1),
				LeaseLength:     int64(2),
			},
		})
	if err != nil {
		return fixture{}, err
	}

	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		return fixture{}, err
	}

	fix.lis = lis

	fix.rpcServer = rpc.NewServer()

	pb.RegisterCapacityServer(fix.rpcServer, fix.server)

	go fix.rpcServer.Serve(lis)

	conn, err := rpc.Dial(fix.Addr(), rpc.WithInsecure())
	if err != nil {
		return fixture{}, err
	}

	fix.client = pb.NewCapacityClient(conn)
	return fix, nil
}

func makeRequest(fix fixture, wants, has float64) (*pb.GetCapacityResponse, error) {
	req := &pb.GetCapacityRequest{
		ClientId: string("client"),
		Resource: []*pb.ResourceRequest{
			{
				ResourceId: string("resource"),
				Priority:   int64(1),
				Has: &pb.Lease{
					ExpiryTime:      int64(0),
					RefreshInterval: int64(0),
					Capacity:        float64(0),
				},
				Wants: float64(wants),
			},
		},
	}

	if has > 0 {
		req.Resource[0].Has = &pb.Lease{
			ExpiryTime:      int64(time.Now().Add(1 * time.Minute).Unix()),
			RefreshInterval: int64(5),
			Capacity:        float64(has),
		}
	}

	return fix.client.GetCapacity(context.Background(), req)
}

type clientWants struct {
	wants      float64
	numClients int64
}

func makeServerRequest(fix fixture, resID string, clients []clientWants, has float64) (*pb.GetServerCapacityResponse, error) {
	var wants []*pb.PriorityBandAggregate
	for _, client := range clients {
		wants = append(wants, &pb.PriorityBandAggregate{
			Priority:   int64(0),
			NumClients: int64(client.numClients),
			Wants:      float64(client.wants),
		})
	}

	req := &pb.GetServerCapacityRequest{
		ServerId: string("server"),
		Resource: []*pb.ServerCapacityResourceRequest{
			{
				ResourceId: string(resID),
				Has: &pb.Lease{
					ExpiryTime:      int64(0),
					RefreshInterval: int64(0),
					Capacity:        float64(0),
				},
				Wants: wants,
			},
		},
	}

	if has > 0 {
		req.Resource[0].Has = &pb.Lease{
			ExpiryTime:      int64(time.Now().Add(1 * time.Minute).Unix()),
			RefreshInterval: int64(1),
			Capacity:        float64(has),
		}
	}

	return fix.client.GetServerCapacity(context.Background(), req)
}

func TestMastership(t *testing.T) {

	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	// FIXME(ryszard): This is necessary because of a race condition
	// - unless we wait, we would be overwritten by the empty
	// election.
	time.Sleep(10 * time.Millisecond)

	fix.server.mu.Lock()
	fix.server.isMaster = false
	fix.server.currentMaster = "somebody-that-i-used-to-know"
	fix.server.mu.Unlock()

	out, err := makeRequest(fix, 10, 0)

	if err != nil {
		t.Errorf("s.GetCapacity: %v", err)
		return
	}
	if len(out.Response) > 0 {
		t.Error("there should be no response from non-master")
	}
	if out.Mastership == nil {
		t.Error("non-master should have information about the master")
	}

	if got, want := out.Mastership.MasterAddress, "somebody-that-i-used-to-know"; got != want {
		t.Errorf("out.Mastership.MasterAddress = %q, want %q", got, want)
	}
}

func TestMasterNotAvailable(t *testing.T) {

	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	// FIXME(ryszard): This is necessary because of a race condition
	// - unless we wait, we would be overwritten by the empty
	// election.
	time.Sleep(10 * time.Millisecond)

	fix.server.mu.Lock()
	fix.server.isMaster = false
	fix.server.currentMaster = "foo"
	fix.server.mu.Unlock()

	res, err := makeRequest(fix, 10, 0)
	if err != nil {
		t.Fatalf("fix.client.GetCapacity(_,_) = res, %v; returned an error", err)
	}
	if res.Mastership == nil {
		t.Fatalf("fix.client.GetCapacity(_,_) = %v,_; did not contain a mastership field", res)
	}
	if res.Mastership.MasterAddress != "foo" {
		t.Fatalf("fix.client.GetCapacity(_,_) = %v,_; did not contain the correct new master", res)
	}
}

func TestLearningMode(t *testing.T) {

	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	out, err := makeRequest(fix, 30, 20)

	if err != nil {
		t.Errorf("s.GetCapacity: %v", err)
		return
	}
	lease := out.Response[0].Gets
	if got, want := lease.Capacity, 20.0; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}

	out, err = makeRequest(fix, 90, 90)
	lease = out.Response[0].Gets

	// We are still in learning mode: the server has overassigned.
	if got, want := lease.Capacity, 90.0; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}

	// Resets the server's start time to 10 minutes in the past and drops the servers
	// resource map to forget history.
	fix.server.mu.Lock()
	fix.server.becameMasterAt = time.Now().Add(-2 * time.Minute)
	fix.server.resources = make(map[string]*Resource)
	fix.server.mu.Unlock()

	out, err = makeRequest(fix, 120, 120)
	lease = out.Response[0].Gets

	// Not in learning mode: the lease should be corrected to
	// avoid overassignment.
	if got, want := lease.Capacity, 100.0; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}
}

func TestEmptyRequest(t *testing.T) {

	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	if res, err := fix.client.GetCapacity(
		context.Background(),
		&pb.GetCapacityRequest{
			ClientId: string("client"),
		}); err != nil {
		t.Fatalf("fix.client.GetCapacity: %v", err)
	} else if len(res.Response) != 0 {
		t.Fatal("Response is not empty")
	}
}

func TestReleaseCapacity(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	_, err = makeRequest(fix, 30, 20)
	if err != nil {
		t.Errorf("s.GetCapacity: %v", err)
		return
	}

	_, err = fix.client.ReleaseCapacity(
		context.Background(),
		&pb.ReleaseCapacityRequest{
			ClientId: string("client"),
			ResourceId: []string{
				"resource",
				"nonexisting_resource",
			}})
	if err != nil {
		t.Fatalf("ReleaseCapacity: %v", err)
	}

	if got, want := fix.server.resources["resource"].store.SumHas(), 0.0; want != got {
		t.Fatalf("res.store.SumHas() = %v, want %v", got, want)
	}
}

func TestLoadConfig(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	// Tries to load an empty config.
	if err := fix.server.LoadConfig(context.Background(), &pb.ResourceRepository{}, map[string]*time.Time{}); err == nil {
		t.Error("fix.server.LoadConfig: no error for bad config")
	}

	_, err = makeRequest(fix, 2000, 20)

	// Give the server a chance to create a resource.
	if err != nil {
		t.Fatalf("s.GetCapacity: %v", err)
	}

	if err := fix.server.LoadConfig(context.Background(), &pb.ResourceRepository{
		Resources: []*pb.ResourceTemplate{
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(100.0),
				Algorithm: &pb.Algorithm{
					Kind:                 pb.Algorithm_NO_ALGORITHM,
					RefreshInterval:      int64(5),
					LeaseLength:          int64(20),
					LearningModeDuration: int64(0),
				},
			},
		},
	}, map[string]*time.Time{}); err != nil {
		t.Fatalf("fix.server.LoadConfig: %v", err)
	}

	out, err := makeRequest(fix, 2000, 20)
	if err != nil {
		t.Fatalf("s.GetCapacity: %v", err)
	}

	lease := out.Response[0].Gets
	if got, want := lease.Capacity, 20.0; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}
}

func TestWrongNumberOfClients(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	clients := []clientWants{
		{
			wants:      10,
			numClients: 0,
		},
	}

	_, err = makeServerRequest(fix, "resource", clients, 0)
	if got, want := rpc.Code(err), codes.InvalidArgument; got != want {
		t.Errorf("s.ServerCapacity = %v, want %v", got, want)
		return
	}
}

func TestGetServerCapacity(t *testing.T) {
	fix, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fix.tearDown()

	// capacity is the maximum capacity of the resource.
	capacity := 100.0

	// Create a resource template.
	if err := fix.server.LoadConfig(context.Background(), &pb.ResourceRepository{
		Resources: []*pb.ResourceTemplate{
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(capacity),
				Algorithm: &pb.Algorithm{
					Kind:                 pb.Algorithm_FAIR_SHARE,
					RefreshInterval:      int64(5),
					LeaseLength:          int64(20),
					LearningModeDuration: int64(-1),
				},
			},
		},
	}, map[string]*time.Time{}); err != nil {
		t.Fatalf("fix.server.LoadConfig: %v", err)
	}

	subclients := 5
	wantsPerSubclient := 200.0
	var clients []clientWants
	// Form pairs of wants capacity and number of subclients.
	for s := 1; s <= subclients; s++ {
		clients = append(clients, clientWants{wantsPerSubclient, int64(s)})
	}

	out, err := makeServerRequest(fix, "resource", clients, 0)
	if err != nil {
		t.Fatalf("s.GetServerCapacity: %v", err)
	}

	// We expect to receive the maximum available capacity.
	lease := out.Response[0].Gets
	if got, want := lease.Capacity, capacity; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}

}

func TestIntermediateServerDefaultLoadConfig(t *testing.T) {
	fixRoot, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fixRoot.tearDown()

	// setUpIntermediate should return successfully, because the default config for
	// intermediate server should be loaded right after the intermediate server
	// is created.
	fixIntermediate, err := setUpIntermediate("testIntermediate", fixRoot.Addr())
	if err != nil {
		t.Errorf("setUpIntermediate: %v", err)
	}

	defer fixIntermediate.tearDown()
}

func TestIntermediateServerUpdate(t *testing.T) {
	fixRoot, err := setUp()
	if err != nil {
		t.Errorf("setUp: %v", err)
	}

	defer fixRoot.tearDown()

	// Define resource id for whose capacity we will be asking.
	resID := "resource"
	// Define the maximum capacity of the resource.
	capacity := 100.0

	// Create a default resource template.
	if err := fixRoot.server.LoadConfig(context.Background(), &pb.ResourceRepository{
		Resources: []*pb.ResourceTemplate{
			{
				IdentifierGlob: string("*"),
				Capacity:       float64(capacity),
				Algorithm: &pb.Algorithm{
					Kind:                 pb.Algorithm_FAIR_SHARE,
					RefreshInterval:      int64(1),
					LeaseLength:          int64(2),
					LearningModeDuration: int64(-1),
				},
			},
		},
	}, map[string]*time.Time{}); err != nil {
		t.Fatalf("fix.server.LoadConfig: %v", err)
	}

	// To avoid waiting for the learning mode's end time, make it less than zero.
	defaultResourceTemplate.Algorithm.LearningModeDuration = -1
	defaultResourceTemplate.Algorithm.RefreshInterval = 1
	defaultResourceTemplate.Algorithm.LeaseLength = 2

	fixIntermediate, err := setUpIntermediate("testIntermediate", fixRoot.Addr())
	if err != nil {
		t.Fatalf("setUpIntermediate: %v", err)
	}

	defer fixIntermediate.tearDown()

	// Form a request for resources.
	clients := []clientWants{
		{
			wants:      capacity,
			numClients: 10,
		},
	}

	out, err := makeServerRequest(fixIntermediate, resID, clients, 0)
	if err != nil {
		t.Fatalf("s.GetServerCapacity: %v", err)
	}

	// We expect to receive zero capacity, because intermediate server
	// has not yet asked a lower-level server to assign a lease for this
	// resource.
	lease := out.Response[0].Gets
	if got, want := lease.Capacity, 0.0; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}

	// Wait for some time, so the intermediate server will manage to perform
	// a request for the resource to the root server.
	// Sleeping time should be more than the refresh interval's duration
	// (which is set to 1 sec in this case), otherwise the intermediate server
	// does not have enough time to request an update of resource configuration
	// from the root server.
	time.Sleep(1500 * time.Millisecond)

	out, err = makeServerRequest(fixIntermediate, resID, clients, 0)
	if err != nil {
		t.Fatalf("s.GetServerCapacity: %v", err)
	}

	// We expect to receive the full capacity, because intermediate server
	// should have asked the root server to assign a lease for this requested
	// resource.
	lease = out.Response[0].Gets
	if got, want := lease.Capacity, capacity; got != want {
		t.Errorf("lease.Capacity = %v, want %v", got, want)
	}
}
