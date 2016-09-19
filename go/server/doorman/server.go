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

// Package doorman is a library implementing global, distributed, client-side
// rate limiting.
//
// This is an experimental and incomplete implementation. Most
// notably, a multi-level tree is not supported, and only the most
// basic algorithms are available.
package doorman

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"doorman/go/connection"
	"doorman/go/server/election"
	"doorman/go/timeutil"
	log "github.com/golang/glog"
	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	rpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "doorman/proto/doorman"
)

var (

	// TODO(rushanny): probably we should get rid of the default vars in the future?

	// defaultPriority is the default priority for the resource request.
	defaultPriority = 1

	// defaultInterval is the default time period after which the server's main loop
	// updates the resources configuration.
	defaultInterval = time.Duration(1 * time.Second)

	// defaultResourceTemplate is the defalt configuration entry for "*" resource.
	defaultResourceTemplate = &pb.ResourceTemplate{
		IdentifierGlob: string("*"),
		Capacity:       float64(0),
		SafeCapacity:   float64(0),
		Algorithm: &pb.Algorithm{
			Kind:                 pb.Algorithm_FAIR_SHARE,
			RefreshInterval:      int64(int64(defaultInterval.Seconds())),
			LeaseLength:          int64(20),
			LearningModeDuration: int64(20),
		},
	}

	// defaultServerCapacityResourceRequest is the default request for "*" resource,
	// which is sent to the lower-level (e.g. root) server only before the server
	// receives actual requests for resources from the clients.
	defaultServerCapacityResourceRequest = &pb.ServerCapacityResourceRequest{
		ResourceId: string("*"),
		Wants: []*pb.PriorityBandAggregate{
			{
				Priority:   int64(int64(defaultPriority)),
				NumClients: int64(1),
				Wants:      float64(0.0),
			},
		},
	}
)

const (
	// veryLongTime is used as a max when looking for a minimum
	// duration.
	veryLongTime = 60 * time.Minute

	// minBackoff is the minimum for the exponential backoff.
	minBackoff = 1 * time.Second

	// maxBackoff is the maximum for the exponential backoff.
	maxBackoff = 1 * time.Minute
)

var (
	requestLabels = []string{"method"}

	requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "doorman",
		Subsystem: "server",
		Name:      "requests",
		Help:      "Requests sent to a Doorman service.",
	}, requestLabels)

	requestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "doorman",
		Subsystem: "server",
		Name:      "request_errors",
		Help:      "Requests sent to a Doorman service that returned an error.",
	}, requestLabels)

	requestDurations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "doorman",
		Subsystem: "server",
		Name:      "request_durations",
		Help:      "Duration of different requests in seconds.",
	}, requestLabels)
)

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(requestErrors)
	prometheus.MustRegister(requestDurations)
}

// Server represents the state of a doorman server.
type Server struct {
	Election election.Election
	ID       string

	// isConfigured is closed once an initial configuration is loaded.
	isConfigured chan bool

	// mu guards all the properties of server.
	mu             sync.RWMutex
	resources      map[string]*Resource
	isMaster       bool
	becameMasterAt time.Time
	currentMaster  string
	config         *pb.ResourceRepository

	// updater updates the resources' configuration for intermediate server.
	// The root server should ignore it, since it loads the resource
	// configuration from elsewhere.
	updater updater

	// conn contains the configuration of the connection between this
	// server and the lower level server if there is one.
	conn *connection.Connection

	// quit is used to notify that the server is to be closed.
	quit chan bool

	// descs are metrics descriptions for use when the server's state
	// is collected by Prometheus.
	descs struct {
		has        *prometheus.Desc
		wants      *prometheus.Desc
		subclients *prometheus.Desc
	}
}

type updater func(server *Server, retryNumber int) (time.Duration, int)

// WaitUntilConfigured blocks until the server is configured. If the server
// is configured to begin with it immediately returns.
func (server *Server) WaitUntilConfigured() {
	<-server.isConfigured
}

// GetLearningModeEndTime returns the timestamp where a resource that has a
// particular learning mode duration leaves learning mode.
// mode duration is still in learning mode.
// Note: If the learningModeDuration is less than zero there is no
// learning mode!
func (server *Server) GetLearningModeEndTime(learningModeDuration time.Duration) time.Time {
	if learningModeDuration.Seconds() < 0 {
		return time.Unix(0, 0)
	}

	return server.becameMasterAt.Add(learningModeDuration)
}

// LoadConfig loads config as the new configuration for the server. It
// will take care of any locking, and it will return an error if the
// config is invalid. LoadConfig takes care of locking the server and
// resources. The first call to LoadConfig also triggers taking part
// in the master election, if the relevant locks were specified when
// the server was created.
func (server *Server) LoadConfig(ctx context.Context, config *pb.ResourceRepository, expiryTimes map[string]*time.Time) error {
	if err := validateResourceRepository(config); err != nil {
		return err
	}

	server.mu.Lock()
	defer server.mu.Unlock()

	firstTime := server.config == nil

	// Stores the new configuration in the server object.
	server.config = config

	// If this is the first load of a config there are no resources
	// in the server map, so no need to process those, but we do need
	// to let people who were waiting on the server configuration
	// known: for this purpose we close isConfigured channel.
	// Also since we are now a configured server we can
	// start participating in the election process.
	if firstTime {
		close(server.isConfigured)
		return server.triggerElection(ctx)
	}

	// Goes through the server's map of resources, loads a new
	// configuration and updates expiration time for each of them.
	for id, resource := range server.resources {
		resource.LoadConfig(server.findConfigForResource(id), expiryTimes[id])
	}

	return nil
}

// performRequests does a request and returns the duration of the
// shortest refresh interval from all handled resources.
//
// If there's an error, it will be logged, and the returned interval
// will be increasing exponentially (basing on the passed retry
// number). The returned nextRetryNumber should be used in the next
// call to performRequests.
func (server *Server) performRequests(ctx context.Context, retryNumber int) (time.Duration, int) {
	// Creates new GetServerCapacityRequest.
	in := &pb.GetServerCapacityRequest{ServerId: string(server.ID)}

	server.mu.RLock()

	// Adds all resources in this client's resource registry to the request.
	for id, resource := range server.resources {
		status := resource.Status()

		// For now we do not take into account clients with different
		// priorities. That is why we form only one PriorityBandAggregate proto.
		// Also, compose request only for the resource whose wants capacity > 0,
		// because it makes no sense to ask for zero capacity.
		if status.SumWants > 0 {
			in.Resource = append(in.Resource, &pb.ServerCapacityResourceRequest{
				ResourceId: string(id),
				// TODO(rushanny): fill optional Has field which is of type Lease.
				Wants: []*pb.PriorityBandAggregate{
					{
						// TODO(rushanny): replace defaultPriority with some client's priority.
						Priority:   int64(int64(defaultPriority)),
						NumClients: int64(status.Count),
						Wants:      float64(status.SumWants),
					},
				},
			})
		}
	}

	// If there is no actual resources that we could ask for, just send a default request
	// just to check a lower-level server's availability.
	if len(server.resources) == 0 {
		in.Resource = append(in.Resource, defaultServerCapacityResourceRequest)
	}
	server.mu.RUnlock()

	if retryNumber > 0 {
		log.Infof("GetServerCapacity: retry number %v: %v\n", retryNumber, in)
	}

	out, err := server.getCapacityRPC(ctx, in)
	if err != nil {
		log.Errorf("GetServerCapacityRequest: %v", err)
		return timeutil.Backoff(minBackoff, maxBackoff, retryNumber), retryNumber + 1
	}

	// Find the minimal refresh interval.
	interval := veryLongTime
	var templates []*pb.ResourceTemplate
	expiryTimes := make(map[string]*time.Time, 0)

	for _, pr := range out.Response {
		_, ok := server.resources[pr.ResourceId]
		if !ok {
			log.Errorf("response for non-existing resource: %q", pr.ResourceId)
			continue
		}

		// Refresh an expiry time for the resource.
		expiryTime := time.Unix(pr.Gets.ExpiryTime, 0)
		expiryTimes[pr.ResourceId] = &expiryTime

		// Add a new resource configuration.
		templates = append(templates, &pb.ResourceTemplate{
			IdentifierGlob: string(pr.ResourceId),
			Capacity:       float64(pr.Gets.Capacity),
			SafeCapacity:   float64(pr.SafeCapacity),
			Algorithm:      pr.Algorithm,
		})

		// Find the minimum refresh interval.
		if refresh := time.Duration(pr.Gets.RefreshInterval) * time.Second; refresh < interval {
			interval = refresh
		}
	}

	// Append the default template for * resource. It should be the last one in templates.
	templates = append(templates, proto.Clone(defaultResourceTemplate).(*pb.ResourceTemplate))

	// Load a new configuration for the resources.
	if err := server.LoadConfig(ctx, &pb.ResourceRepository{
		Resources: templates,
	}, expiryTimes); err != nil {
		log.Errorf("server.LoadConfig: %v", err)
		return timeutil.Backoff(minBackoff, maxBackoff, retryNumber), retryNumber + 1
	}

	// Applies the --minimum_refresh_interval_secs flag.
	// Or if interval was set to veryLongTime and not updated, set it to minimum refresh interval.
	if interval < server.conn.Opts.MinimumRefreshInterval || interval == veryLongTime {
		log.Infof("overriding interval %v with %v", interval, server.conn.Opts.MinimumRefreshInterval)
		interval = server.conn.Opts.MinimumRefreshInterval
	}

	return interval, 0
}

// getCapacityRPC Executes this RPC against the current master. Returns the GetServerCapacity RPC
// response, or nil if an error occurred.
func (server *Server) getCapacityRPC(ctx context.Context, in *pb.GetServerCapacityRequest) (*pb.GetServerCapacityResponse, error) {
	out, err := server.conn.ExecuteRPC(func() (connection.HasMastership, error) {
		return server.conn.Stub.GetServerCapacity(ctx, in)

	})

	// Returns an error if we could not execute the RPC.
	if err != nil {
		return nil, err
	}

	// Returns the result from the RPC to the caller.
	return out.(*pb.GetServerCapacityResponse), err
}

// IsMaster returns true if server is the master.
func (server *Server) IsMaster() bool {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.isMaster
}

// CurrentMaster returns the current master, or an empty string if
// there's no master or it is unknown.
func (server *Server) CurrentMaster() string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.currentMaster
}

func validateGetCapacityRequest(p *pb.GetCapacityRequest) error {
	if p.ClientId == "" {
		return errors.New("client_id cannot be empty")
	}
	seen := make(map[string]bool)
	for _, r := range p.Resource {
		if err := validateResourceRequest(r); err != nil {
			return err
		}
		seen[r.ResourceId] = true
	}

	return nil
}

func validateResourceRequest(p *pb.ResourceRequest) error {
	if p.ResourceId == "" {
		return errors.New("resource_id cannot be empty")
	}
	if p.Wants < 0 {
		return errors.New("capacity must be positive")
	}
	return nil
}

// validateResourceRepository returns an error if p is not correct. It
// must contain an entry for "*" which must also be the last entry.
func validateResourceRepository(p *pb.ResourceRepository) error {
	starFound := false
	for i, res := range p.Resources {
		glob := res.IdentifierGlob
		// All globs have to be well formed.
		//
		// NOTE(ryszard): filepath.Match will NOT return an
		// error if the glob is matched against the empty
		// string.
		if _, err := filepath.Match(glob, " "); err != nil {
			return err
		}

		// If there is an algorithm in this entry, validate it.
		if algo := res.Algorithm; algo != nil {
			if algo.RefreshInterval == 0 || algo.LeaseLength == 0 {
				return errors.New("must have a refresh interval and a lease length")
			}

			if algo.RefreshInterval < 1 {
				return errors.New("invalid refresh interval, must be at least 1 second")
			}

			if algo.LeaseLength < 1 {
				return errors.New("Invalid lease length, must be at least 1 second")
			}

			if algo.LeaseLength < algo.RefreshInterval {
				return errors.New("Lease length must be larger than the refresh interval")
			}
		}

		// * has to contain an algorithm and be the last
		// entry.
		if glob == "*" {
			if res.Algorithm == nil {
				return errors.New("the entry for * must specify an algorithm")
			}
			if i+1 != len(p.Resources) {
				return errors.New(`the entry for "*" must be the last one`)
			}
			starFound = true
		}
	}

	if !starFound {
		return errors.New(`the resource respository must contain at least an entry for "*"`)
	}

	return nil
}

// handleElectionOutcome observes the results of master elections and
// updates the server to reflect acquired or lost mastership.
func (server *Server) handleElectionOutcome() {
	for isMaster := range server.Election.IsMaster() {
		server.mu.Lock()
		server.isMaster = isMaster

		if isMaster {
			log.Info("this Doorman server is now the master")
			server.becameMasterAt = time.Now()
			server.resources = make(map[string]*Resource)
		} else {
			log.Warning("this Doorman server lost mastership")
			server.becameMasterAt = time.Unix(0, 0)
			server.resources = nil
		}

		server.mu.Unlock()
	}
}

// handleMasterID observes the IDs of elected masters and makes them
// available through CurrentMaster.
func (server *Server) handleMasterID() {
	for newMaster := range server.Election.Current() {
		server.mu.Lock()

		if newMaster != server.currentMaster {
			log.Infof("setting current master to '%v'", newMaster)
			server.currentMaster = newMaster
		}

		server.mu.Unlock()
	}
}

// triggerElection makes the server run in a Chubby Master2 election.
func (server *Server) triggerElection(ctx context.Context) error {
	if err := server.Election.Run(ctx, server.ID); err != nil {
		return err
	}
	go server.handleElectionOutcome()
	go server.handleMasterID()

	return nil
}

// New returns a new unconfigured server. parentAddr is the address of
// a parent, pass the empty string to create a root server. This
// function should be called only once, as it registers metrics.
func New(ctx context.Context, id string, parentAddr string, leader election.Election, opts ...connection.Option) (*Server, error) {
	s, err := NewIntermediate(ctx, id, parentAddr, leader, opts...)
	if err != nil {
		return nil, err
	}

	return s, prometheus.Register(s)
}

// Describe implements prometheus.Collector.
func (server *Server) Describe(ch chan<- *prometheus.Desc) {
	ch <- server.descs.has
	ch <- server.descs.wants
	ch <- server.descs.subclients
}

// Collect implements prometheus.Collector.
func (server *Server) Collect(ch chan<- prometheus.Metric) {
	status := server.Status()

	for id, res := range status.Resources {
		ch <- prometheus.MustNewConstMetric(server.descs.has, prometheus.GaugeValue, res.SumHas, id)
		ch <- prometheus.MustNewConstMetric(server.descs.wants, prometheus.GaugeValue, res.SumWants, id)
		ch <- prometheus.MustNewConstMetric(server.descs.subclients, prometheus.GaugeValue, float64(res.Count), id)
	}
}

// NewIntermediate creates a server connected to the lower level server.
func NewIntermediate(ctx context.Context, id string, addr string, leader election.Election, opts ...connection.Option) (*Server, error) {
	var (
		conn    *connection.Connection
		updater updater
		err     error
	)

	isRootServer := addr == ""

	// Set up some configuration for intermediate server: establish a connection
	// to a lower-level server (e.g. the root server) and assign the updater function.
	if !isRootServer {
		if conn, err = connection.New(addr, opts...); err != nil {
			return nil, err
		}

		updater = func(server *Server, retryNumber int) (time.Duration, int) {
			return server.performRequests(ctx, retryNumber)
		}
	}

	server := &Server{
		ID:             id,
		Election:       leader,
		isConfigured:   make(chan bool),
		resources:      make(map[string]*Resource),
		becameMasterAt: time.Unix(0, 0),
		conn:           conn,
		updater:        updater,
		quit:           make(chan bool),
	}

	const (
		namespace = "doorman"
		subsystem = "server"
	)

	labelNames := []string{"resource"}
	server.descs.has = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, subsystem, "has"),
		"All capacity assigned to clients for a resource.",
		labelNames, nil,
	)
	server.descs.wants = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, subsystem, "wants"),
		"All capacity requested by clients for a resource.",
		labelNames, nil,
	)
	server.descs.subclients = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, subsystem, "subclients"),
		"Number of clients requesting this resource.",
		labelNames, nil,
	)

	// For an intermediate server load the default config for "*"
	// resource.  As for root server, this config will be loaded
	// from some external source..
	if !isRootServer {
		if err := server.LoadConfig(ctx, &pb.ResourceRepository{
			Resources: []*pb.ResourceTemplate{
				proto.Clone(defaultResourceTemplate).(*pb.ResourceTemplate),
			},
		}, map[string]*time.Time{}); err != nil {
			return nil, err
		}
	}

	go server.run()

	return server, nil
}

// run is the server's main loop. It takes care of requesting new resources,
// and managing ones already claimed. This is the only method that should be
// performing RPC.
func (server *Server) run() {
	interval := defaultInterval
	retryNumber := 0

	for {
		var wakeUp <-chan time.Time
		if server.updater != nil {
			wakeUp = time.After(interval)
		}

		select {
		case <-server.quit:
			// The server is closed, nothing to do here.
			return
		case <-wakeUp:
			// Time to update the resources configuration.
			interval, retryNumber = server.updater(server, retryNumber)
		}
	}
}

// Close closes the doorman server.
func (server *Server) Close() {
	server.quit <- true
}

// findConfigForResource find the configuration template that applies
// to a specific resource. This function panics if if cannot find a
// suitable template, which should never happen because there is always
// a configuration entry for "*".
func (server *Server) findConfigForResource(id string) *pb.ResourceTemplate {
	// Try to match it literally.
	for _, tpl := range server.config.Resources {
		if tpl.IdentifierGlob == id {
			return tpl
		}
	}

	// See if there's a template that matches as a pattern.
	for _, tpl := range server.config.Resources {
		glob := tpl.IdentifierGlob
		matched, err := filepath.Match(glob, id)

		if err != nil {
			log.Errorf("Error trying to match %v to %v", id, glob)
			continue
		} else if matched {
			return tpl
		}
	}

	// This should never happen
	panic(id)
}

// getResource takes a resource identifier and returns the matching
// resource (which will be created if necessary).
func (server *Server) getOrCreateResource(id string) *Resource {
	server.mu.Lock()
	defer server.mu.Unlock()

	// Resource already exists in the server state; return it.
	if res, ok := server.resources[id]; ok {
		return res
	}

	resource := server.newResource(id, server.findConfigForResource(id))
	server.resources[id] = resource

	return resource
}

// ReleaseCapacity releases capacity owned by a client.
func (server *Server) ReleaseCapacity(ctx context.Context, in *pb.ReleaseCapacityRequest) (out *pb.ReleaseCapacityResponse, err error) {
	out = new(pb.ReleaseCapacityResponse)

	log.V(2).Infof("ReleaseCapacity req: %v", in)
	start := time.Now()
	requests.WithLabelValues("ReleaseCapacity").Inc()
	defer func() {
		log.V(2).Infof("ReleaseCapacity res: %v", out)
		requestDurations.WithLabelValues("ReleaseCapacity").Observe(time.Since(start).Seconds())
		if err != nil {
			requestErrors.WithLabelValues("ReleaseCapacity").Inc()

		}
	}()

	// If we are not the master we tell the client who we think the master
	// is and we return. There are some subtleties around this: The presence
	// of the mastership field signifies that we are not the master. The
	// presence of the master_bns field inside mastership signifies whether
	// we know who the master is or not.
	if !server.IsMaster() {
		out.Mastership = &pb.Mastership{}

		if server.currentMaster != "" {
			out.Mastership.MasterAddress = string(server.currentMaster)
		}

		return out, nil
	}

	client := in.ClientId

	// Takes the server lock because we are reading the resource map below.
	server.mu.RLock()
	defer server.mu.RUnlock()

	for _, resourceID := range in.ResourceId {
		// If the server does not know about the resource we don't have to do
		// anything.
		if res, ok := server.resources[resourceID]; ok {
			res.store.Release(client)
		}
	}

	return out, nil
}

// item is the mapping between the client id and the lease that algorithm assigned to the client with this id.
type item struct {
	id    string
	lease Lease
}

type clientRequest struct {
	client     string
	resID      string
	has        float64
	wants      float64
	subclients int64
}

// GetCapacity assigns capacity leases to clients. It is part of the
// doorman.CapacityServer implementation.
func (server *Server) GetCapacity(ctx context.Context, in *pb.GetCapacityRequest) (out *pb.GetCapacityResponse, err error) {
	out = new(pb.GetCapacityResponse)

	log.V(2).Infof("GetCapacity req: %v", in)

	start := time.Now()
	requests.WithLabelValues("GetCapacity").Inc()
	defer func() {
		log.V(2).Infof("GetCapacity res: %v", out)
		requestDurations.WithLabelValues("GetCapacity").Observe(time.Since(start).Seconds())
		if err != nil {
			requestErrors.WithLabelValues("GetCapacity").Inc()
		}
	}()

	// If we are not the master, we redirect the client.
	if !server.IsMaster() {
		master := server.CurrentMaster()
		out.Mastership = &pb.Mastership{}

		if master != "" {
			out.Mastership.MasterAddress = string(master)
		}
		return out, nil
	}

	client := in.ClientId

	// We will create a new goroutine for every resource in the
	// request. This is the channel that the leases come back on.
	itemsC := make(chan item, len(in.Resource))

	// requests will keep information about all resource requests that
	// the specified client sent at the moment.
	var requests []clientRequest

	for _, req := range in.Resource {
		_has_capacity := 0.0
		if has := req.GetHas(); has != nil {
			_has_capacity = has.Capacity
		}
		request := clientRequest{
			client:     client,
			resID:      req.ResourceId,
			has:        _has_capacity,
			wants:      req.Wants,
			subclients: 1,
		}

		requests = append(requests, request)
	}

	server.getCapacity(requests, itemsC)

	// We collect the assigned leases.
	for range in.Resource {
		item := <-itemsC
		resp := &pb.ResourceResponse{
			ResourceId: string(item.id),
			Gets: &pb.Lease{
				RefreshInterval: int64(int64(item.lease.RefreshInterval.Seconds())),
				ExpiryTime:      int64(item.lease.Expiry.Unix()),
				Capacity:        float64(item.lease.Has),
			},
		}
		server.getOrCreateResource(item.id).SetSafeCapacity(resp)
		out.Response = append(out.Response, resp)
	}

	return out, nil
}

func (server *Server) getCapacity(crequests []clientRequest, itemsC chan item) {
	for _, creq := range crequests {
		res := server.getOrCreateResource(creq.resID)
		req := Request{
			Client:     creq.client,
			Has:        creq.has,
			Wants:      creq.wants,
			Subclients: creq.subclients,
		}

		go func(req Request) {
			itemsC <- item{
				id:    res.ID,
				lease: res.Decide(req),
			}
		}(req)
	}
}

// GetServerCapacity gives capacity to doorman servers that can assign
// to their clients. It is part of the doorman.CapacityServer
// implementation.
func (server *Server) GetServerCapacity(ctx context.Context, in *pb.GetServerCapacityRequest) (out *pb.GetServerCapacityResponse, err error) {
	out = new(pb.GetServerCapacityResponse)
	// TODO: add metrics for getServerCapacity latency and requests count.
	log.V(2).Infof("GetServerCapacity req: %v", in)
	defer log.V(2).Infof("GetServerCapacity res: %v", out)

	// If we are not the master, we redirect the client.
	if !server.IsMaster() {
		master := server.CurrentMaster()
		out.Mastership = &pb.Mastership{}

		if master != "" {
			out.Mastership.MasterAddress = string(master)
		}

		return out, nil
	}

	client := in.ServerId

	// We will create a new goroutine for every resource in the
	// request. This is the channel that the leases come back on.
	itemsC := make(chan item, len(in.Resource))

	// requests will keep information about all resource requests that
	// the specified client sent at the moment.
	var requests []clientRequest

	for _, req := range in.Resource {
		var (
			wantsTotal      float64
			subclientsTotal int64
		)

		// Calaculate total number of subclients and overall wants
		// capacity that they ask for.
		for _, wants := range req.Wants {
			wantsTotal += wants.Wants

			// Validate number of subclients which should be not less than 1,
			// because every server has at least one subclient: itself.
			subclients := wants.NumClients
			if subclients < 1 {
				return nil, rpc.Errorf(codes.InvalidArgument, "subclients should be > 0")
			}
			subclientsTotal += wants.NumClients
		}

		_has_capacity := 0.0
		if has := req.GetHas(); has != nil {
			_has_capacity = has.Capacity
		}
		request := clientRequest{
			client:     client,
			resID:      req.ResourceId,
			has:        _has_capacity,
			wants:      wantsTotal,
			subclients: subclientsTotal,
		}

		requests = append(requests, request)
	}

	server.getCapacity(requests, itemsC)

	// We collect the assigned leases.
	for range in.Resource {
		item := <-itemsC
		resp := &pb.ServerCapacityResourceResponse{
			ResourceId: string(item.id),
			Gets: &pb.Lease{
				RefreshInterval: int64(int64(item.lease.RefreshInterval.Seconds())),
				ExpiryTime:      int64(item.lease.Expiry.Unix()),
				Capacity:        float64(item.lease.Has),
			},
			Algorithm:    server.resources[item.id].config.Algorithm,
			SafeCapacity: float64(server.resources[item.id].config.SafeCapacity),
		}

		out.Response = append(out.Response, resp)
	}

	return out, nil
}

// Discovery implements the Discovery RPC which can be used to discover the address of the master.
func (server *Server) Discovery(ctx context.Context, in *pb.DiscoveryRequest) (out *pb.DiscoveryResponse, err error) {
	out = new(pb.DiscoveryResponse)

	out.IsMaster = server.isMaster
	out.Mastership = &pb.Mastership{}
	master := server.CurrentMaster()

	if master != "" {
		out.Mastership.MasterAddress = string(master)
	}

	return out, nil
}

// ServerStatus is a read-only view of a server suitable for
// reporting, eg in /statusz.
type ServerStatus struct {
	// IsMaster is true if the server is a master.
	IsMaster bool
	// Election contains information related to the master  election.
	Election election.Election
	// CurrentMaster is the id of the current master.
	CurrentMaster string
	// Resources are the statuses of the resources managed by this
	// server.
	Resources map[string]ResourceStatus
	// Config is the human readable representation of this server's
	// config.
	Config string
}

// Status returns a read-only view of server.
func (server *Server) Status() ServerStatus {
	server.mu.RLock()
	defer server.mu.RUnlock()
	resources := make(map[string]ResourceStatus, len(server.resources))

	for k, v := range server.resources {
		resources[k] = v.Status()
	}
	return ServerStatus{
		IsMaster:      server.isMaster,
		Election:      server.Election,
		CurrentMaster: server.currentMaster,
		Resources:     resources,
		Config:        proto.MarshalTextString(server.config),
	}
}

// ResourceLeaseStatus returns a read-only view of of the leases on a resource owned by this server.
func (server *Server) ResourceLeaseStatus(id string) ResourceLeaseStatus {
	server.mu.RLock()
	defer server.mu.RUnlock()
	res, ok := server.resources[id]
	if !ok {
		log.Errorf("ResourceLeaseStatus: no resource with ID %v. Returning empty status.", id)
		return ResourceLeaseStatus{}
	}
	return res.ResourceLeaseStatus()
}
