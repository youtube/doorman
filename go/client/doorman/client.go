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

// Package doorman is a client library for Doorman, a global, distributed,
// client side rate limitting service.
package doorman

// TODO(ryszard): Handle safe capacities.

// TODO(ryszard): Monitoring.

// TODO(ryszard): Rate limiters that base on Resource.

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"doorman/go/connection"
	"doorman/go/timeutil"
	log "github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	rpc "google.golang.org/grpc"

	pb "doorman/proto/doorman"
)

const (
	capacityChannelSize = 32

	// veryLongTime is used as a max when looking for a minimum
	// duration.
	veryLongTime = 60 * time.Minute

	// minBackoff is the minimum for the exponential backoff.
	minBackoff = 1 * time.Second

	// maxBackoff is the maximum for the exponential backoff.
	maxBackoff = 1 * time.Minute
)

var (
	// idCount is a counter used to generate unique ids for Doorman clients.
	idCount int32

	// ErrDuplicateResourceID is an error indicating the requested
	// resource was already claimed from this client.
	ErrDuplicateResourceID = errors.New("duplicate resource ID")

	// ErrInvalidWants indicates that wants must be a postive number > 0
	ErrInvalidWants = errors.New("wants must be > 0.0")
)

var (
	requestLabels = []string{"server", "method"}

	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "doorman",
		Subsystem: "client",
		Name:      "requests",
		Help:      "Requests sent to a Doorman service.",
	}, requestLabels)

	requestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "doorman",
		Subsystem: "client",
		Name:      "request_errors",
		Help:      "Requests sent to a Doorman service that returned an error.",
	}, requestLabels)

	requestDurations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "doorman",
		Subsystem: "client",
		Name:      "request_durations",
		Help:      "Duration of different requests in seconds.",
	}, requestLabels)
)

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(requestErrors)
	prometheus.MustRegister(requestDurations)
}

// NOTE: We're wrapping connection package's functions and types here in the client,
// because we do not want our users to be aware of the internal connection package.

// Option configures the client's connection parameters.
type Option connection.Option

// getClientID returns a unique client id, consisting of a host:pid id plus a counter
func getClientID() string {
	hostname, err := os.Hostname()

	if err != nil {
		panic(fmt.Sprintf("Can't determine hostname: %v", err))
	}

	return fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), idCount)
}

// MinimumRefreshInterval sets the minimum refresh interval for
// establishing the client's connection with the server.
func MinimumRefreshInterval(t time.Duration) Option {
	return Option(connection.MinimumRefreshInterval(t))
}

// DialOpts sets dial options for the client's connection with the server.
func DialOpts(dialOpts ...rpc.DialOption) Option {
	return Option(connection.DialOpts(dialOpts...))
}

// Resource represents a resource managed by a doorman server.
type Resource interface {
	// Capacity returns a channel on which the available capacity
	// will be sent.
	Capacity() chan float64

	// Ask requests a new capacity for this resource. If the resource
	// was already released this call has no effect.
	Ask(float64) error

	// Release releases any capacity held by this client.
	Release() error
}

// resourceAction is a message that encapsulates a resource and
// an error channel. It gets sent to the client goroutine over
// one of the action channels.
type resourceAction struct {
	resource *resourceImpl

	// err is the channel over which the result of the requested
	// action should be sent.
	err chan error
}

// Client is a Doorman client.
type Client struct {
	id string

	// conn keeps information about this client's connection to the server.
	conn *connection.Connection

	// resources keeps currently claimed resources.
	resources map[string]*resourceImpl

	// Channels for synchronization with the client's goroutine.
	newResource     chan resourceAction
	releaseResource chan resourceAction
	goRoutineHalted chan bool
}

// New creates a new client connected to server available at addr. It
// will use the hostname to generate a client id. If you need finer
// control over the client's id, use NewWithID.
func New(addr string, opts ...Option) (*Client, error) {
	return NewWithID(addr, getClientID(), opts...)
}

// NewWithID creates a new client connected to server available at
// addr, identifying using the custom id provided.
func NewWithID(addr string, id string, opts ...Option) (*Client, error) {
	var connectionOpts []connection.Option
	for _, opt := range opts {
		connectionOpts = append(connectionOpts, connection.Option(opt))
	}

	conn, err := connection.New(addr, connectionOpts...)
	if err != nil {
		return nil, err
	}

	client := &Client{
		id:              id,
		conn:            conn,
		resources:       make(map[string]*resourceImpl),
		newResource:     make(chan resourceAction),
		releaseResource: make(chan resourceAction),
		goRoutineHalted: make(chan bool),
	}

	go client.run()

	return client, nil
}

// GetMaster returns the address of the Doorman master we are connected to.
func (client *Client) GetMaster() string {
	if client.conn == nil {
		return ""
	}

	return client.conn.String()
}

// run is the client's main loop. It takes care of requesting new
// resources, and managing ones already claimed. This is the only
// method that should be modifying the client's internal state and
// performing RPCs.
func (client *Client) run() {
	var (
		wakeUp     = make(chan bool, 1)
		timer      *time.Timer
		retryCount = 0
	)

	defer close(client.goRoutineHalted)

	for {
		// The request is made when we get out of this
		// select. This happens in three cases: a new resource
		// was requested, a resource was released, or the wakeup
		// timer has fired.
		select {
		case req, ok := <-client.newResource:
			if !ok {
				// The client has closed, nothing to do here.
				return
			}

			req.err <- client.addResource(req.resource)

		case req, ok := <-client.releaseResource:
			if !ok {
				// The client has closed, nothing to do here.
				return
			}

			req.err <- client.removeResource(req.resource)
			continue

		case <-wakeUp:
			// The timer has fired, which means that at least one
			// refresh interval has expired. If that is the case
			// we are done with the timer.
			timer = nil
		}

		// At this point either a resource action was performed or
		// a refresh interval expired. If it is the former we
		// need to stop the timer and potentially empty the wakeup
		// channel if it fired in between (race condition).
		if timer != nil && !timer.Stop() {
			<-wakeUp
			timer = nil
		}

		// Perform any request that need to be performed. It returns
		// the time we need to sleep until the next action. This can
		// either be a refresh interval, or an exponential backoff in
		// case of errors.
		var interval time.Duration

		interval, retryCount = client.performRequests(retryCount)

		// Creates a new timer which will wake us up after the
		// specified interval has expired.
		timer = time.AfterFunc(interval, func() {
			wakeUp <- true
		})
	}
}

func (client *Client) addResource(res *resourceImpl) error {
	if _, ok := client.resources[res.id]; ok {
		return ErrDuplicateResourceID
	}
	client.resources[res.id] = res
	return nil
}

func (client *Client) removeResource(res *resourceImpl) error {
	if _, ok := client.resources[res.id]; !ok {
		return nil
	}

	delete(client.resources, res.id)
	close(res.capacity)

	in := &pb.ReleaseCapacityRequest{
		ClientId: string(client.id),
		ResourceId: []string{
			res.id,
		}}
	_, err := client.releaseCapacity(in)

	return err
}

// performRequests does a request and returns the duration of the
// shortest refresh interval from all handled resources.
//
// If there's an error, it will be logged, and the returned interval
// will be increasing exponentially (basing on the passed retry
// number). The returned nextRetryNumber should be used in the next
// call to performRequests.
func (client *Client) performRequests(retryNumber int) (interval time.Duration, nextRetryNumber int) {
	// Creates new GetCapacityRequest
	in := &pb.GetCapacityRequest{ClientId: string(client.id)}

	// Adds all resources in this client's resource registry to the
	// request.
	for id, resource := range client.resources {
		in.Resource = append(in.Resource, &pb.ResourceRequest{
			Priority:   int64(resource.priority),
			ResourceId: string(id),
			Wants:      float64(resource.Wants()),
			Has:        resource.lease,
		})
	}

	if retryNumber > 0 {
		log.Infof("GetCapacity: retry number %v: %v", retryNumber, in)
	}

	out, err := client.getCapacity(in)

	if err != nil {
		log.Errorf("GetCapacityRequest: %v", err)

		// Expired resources only need to be handled if the
		// RPC failed: otherwise the client has gotten a
		// refreshed lease.
		for _, res := range client.resources {
			if res.expires().Before(time.Now()) {
				res.lease = nil
				// FIXME(ryszard): This probably should be the safe
				// capacity instead.
				res.capacity <- 0.0
			}
		}
		return timeutil.Backoff(minBackoff, maxBackoff, retryNumber), retryNumber + 1
	}

	for _, pr := range out.Response {
		res, ok := client.resources[pr.ResourceId]

		if !ok {
			log.Errorf("response for non-existing resource: %q", pr.ResourceId)
			continue
		}

		oldCapacity := float64(-1)

		if res.lease != nil {
			oldCapacity = res.lease.Capacity
		}

		res.lease = pr.Gets

		// Only send a message down the channel if the capacity has changed.
		if res.lease.Capacity != oldCapacity {
			// res.capacity is a buffered channel, so if no one is
			// receiving on the other side this will send messages
			// over it until it reaches its size, and then will
			// start dropping them.
			select {
			case res.capacity <- res.lease.Capacity:
			default:
			}
		}
	}

	// Finds the minimal refresh interval.
	interval = veryLongTime

	for _, res := range client.resources {
		if refresh := time.Duration(res.lease.RefreshInterval) * time.Second; refresh < interval {
			interval = refresh
		}
	}

	// Applies the --minimum_refresh_interval_secs flag.
	if interval < client.conn.Opts.MinimumRefreshInterval {
		log.Infof("overriding interval %v with %v", interval, client.conn.Opts.MinimumRefreshInterval)
		interval = client.conn.Opts.MinimumRefreshInterval
	}

	return interval, 0
}

// Resource requests capacity from the resource identified by id and
// with priority 0. If the resource with this id has been already
// claimed from this client, it will return ErrDuplicateResourceID.
func (client *Client) Resource(id string, capacity float64) (Resource, error) {
	return client.ResourceWithPriority(id, capacity, 0)
}

// ResourceWithPriority requests capacity from the resource identified
// by id, with the provided priority.
func (client *Client) ResourceWithPriority(id string, capacity float64, priority int64) (Resource, error) {
	res := &resourceImpl{
		id:       id,
		wants:    capacity,
		priority: priority,
		capacity: make(chan float64, capacityChannelSize),
		client:   client,
	}

	errC := make(chan error)
	client.newResource <- resourceAction{err: errC, resource: res}
	return res, <-errC
}

// Close closes the doorman client.
func (client *Client) Close() {
	// TODO: Make this method idempotent
	close(client.newResource)
	close(client.releaseResource)

	<-client.goRoutineHalted

	in := &pb.ReleaseCapacityRequest{
		ClientId:   string(client.id),
		ResourceId: make([]string, len(client.resources)),
	}

	for id, res := range client.resources {
		in.ResourceId = append(in.ResourceId, id)
		close(res.capacity)
	}

	client.releaseCapacity(in)
	// close closes the connection of the client to the server.
	client.conn.Close()
}

// releaseCapacity executes this RPC against the current master.
func (client *Client) releaseCapacity(in *pb.ReleaseCapacityRequest) (*pb.ReleaseCapacityResponse, error) {
	// context.TODO(ryszard): Plumb a context through.
	out, err := client.conn.ExecuteRPC(func() (connection.HasMastership, error) {
		start := time.Now()
		requests.WithLabelValues(client.conn.String(), "ReleaseCapacity").Inc()
		defer requestDurations.WithLabelValues(client.conn.String(), "ReleaseCapacity").Observe(time.Since(start).Seconds())

		return client.conn.Stub.ReleaseCapacity(context.TODO(), in)

	})

	// Returns an error if we could not execute the RPC.
	if err != nil {
		requestErrors.WithLabelValues(client.conn.String(), "ReleaseCapacity").Inc()
		return nil, err
	}

	// Returns the result from the RPC to the caller.
	return out.(*pb.ReleaseCapacityResponse), err
}

// getCapacity Executes this RPC against the current master. Returns the GetCapacity RPC
// response, or nil if an error occurred.
func (client *Client) getCapacity(in *pb.GetCapacityRequest) (*pb.GetCapacityResponse, error) {
	// context.TODO(ryszard): Plumb a context through.
	out, err := client.conn.ExecuteRPC(func() (connection.HasMastership, error) {
		start := time.Now()
		requests.WithLabelValues(client.conn.String(), "GetCapacity").Inc()
		defer func() {
			log.Infof("%v %v", time.Since(start).Seconds(), time.Since(start))
			requestDurations.WithLabelValues(client.conn.String(), "GetCapacity").Observe(time.Since(start).Seconds())
		}()
		return client.conn.Stub.GetCapacity(context.TODO(), in)

	})

	// Returns an error if we could not execute the RPC.
	if err != nil {
		requestErrors.WithLabelValues(client.conn.String(), "GetCapacity").Inc()
		return nil, err
	}

	// Returns the result from the RPC to the caller.
	return out.(*pb.GetCapacityResponse), err
}

// resourceImpl is the implementation of Resource.
type resourceImpl struct {
	id       string
	priority int64
	capacity chan float64
	client   *Client

	// lease is the currently assigned lease.
	lease *pb.Lease

	// mu guards access to wants.
	mu    sync.Mutex
	wants float64
}

// expires returns the time at which the lease for this resource will
// expire.
func (res *resourceImpl) expires() time.Time {
	return time.Unix(res.lease.ExpiryTime, 0)
}

// Capacity implements the Resource interface.
func (res *resourceImpl) Capacity() chan float64 {
	return res.capacity
}

// Wants returns the currently desired capacity. It takes care of
// locking the resource.
func (res *resourceImpl) Wants() float64 {
	res.mu.Lock()
	defer res.mu.Unlock()

	return res.wants
}

// Ask implements the Resource interface.
func (res *resourceImpl) Ask(wants float64) error {
	if wants <= 0 {
		return ErrInvalidWants
	}

	res.mu.Lock()
	defer res.mu.Unlock()
	res.wants = wants

	return nil
}

// Release implements the Resource interface.
func (res *resourceImpl) Release() error {
	errC := make(chan error)
	res.client.releaseResource <- resourceAction{err: errC, resource: res}

	return <-errC
}
