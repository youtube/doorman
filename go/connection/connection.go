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

// Package connection provides functionality to establish
// the connection between the client and the server. It is
// an internal package to be used only by the Doorman system.
package connection

import (
	"time"

	"doorman/go/timeutil"
	log "github.com/golang/glog"
	rpc "google.golang.org/grpc"

	pb "doorman/proto/doorman"
)

const (
	maxRetries = 5

	// minBackoff is the minimum for the exponential backoff.
	minBackoff = 1 * time.Second

	// maxBackoff is the maximum for the exponential backoff.
	maxBackoff = 1 * time.Minute
)

// Connection contains information about connection between the server and the client.
type Connection struct {
	addr          string
	currentMaster string
	Stub          pb.CapacityClient
	conn          *rpc.ClientConn
	Opts          *Options
}

func (connection *Connection) String() string {
	return connection.currentMaster
}

// New creates a new Connection with the given server address.
func New(addr string, options ...Option) (*Connection, error) {
	connection := &Connection{
		addr: addr,
		Opts: getOptions(options),
	}

	if err := connection.connect(addr); err != nil {
		return nil, err
	}

	return connection, nil
}

// Options keeps information about connection configuration.
type Options struct {
	DialOpts               []rpc.DialOption
	MinimumRefreshInterval time.Duration
}

// Option configures the connection parameters.
type Option func(*Options)

func getOptions(options []Option) *Options {
	opts := &Options{
		MinimumRefreshInterval: 5 * time.Second,
	}

	for _, opt := range options {
		opt(opts)
	}

	return opts
}

// MinimumRefreshInterval sets the minimum refresh interval for
// the connection's establishing.
func MinimumRefreshInterval(t time.Duration) Option {
	return func(opts *Options) {
		opts.MinimumRefreshInterval = t
	}
}

// DialOpts sets dial options for the connection.
func DialOpts(dialOpts ...rpc.DialOption) Option {
	return func(opts *Options) {
		opts.DialOpts = dialOpts
	}
}

// connect connects the client to the server at addr.
func (connection *Connection) connect(addr string) error {
	connection.Close()
	log.Infof("connecting to %v", addr)

	conn, err := rpc.Dial(addr, connection.Opts.DialOpts...)
	if err != nil {
		log.Errorf("connection failed: %v", err)
		return err
	}

	connection.conn, connection.Stub = conn, pb.NewCapacityClient(conn)
	connection.currentMaster = addr

	return nil
}

// ExecuteRPC executes an RPC against the current master.
func (connection *Connection) ExecuteRPC(callback func() (HasMastership, error)) (interface{}, error) {
	// Runs the actual RPC (through the callback function passed in here)
	// through the runMasterAware shell.
	return connection.runMasterAware(callback)
}

// HasMastership is an interface that is implemented by RPC responses
// that may contain changing mastership information.
type HasMastership interface {
	GetMastership() *pb.Mastership
}

// runMasterAware is a wrapper for RPCs that may receive a response informing
// of a changed mastership, in which case it will reconnect and retry.
func (connection *Connection) runMasterAware(callback func() (HasMastership, error)) (interface{}, error) {
	var (
		err     error
		out     HasMastership
		retries int
	)

	for {
		// Does the exponential backoff sleep.
		if retries > 0 {
			t := timeutil.Backoff(minBackoff, maxBackoff, retries)
			log.Infof("retry sleep number %d: %v", retries, t)
			time.Sleep(t)
		}

		retries++

		// We goto here when we want to retry the loop without sleeping.
	RetryNoSleep:

		// If there is no current client connection, connect to the original target.
		// If that fails, retry.
		if connection.conn == nil {
			if err := connection.connect(connection.addr); err != nil {
				// The connection failed. Retry.
				continue
			}
		}

		// Calls the callback function that performs an RPC on the master.
		out, err = callback()

		// If an error happened we are going to close the connection to the
		// server. The next iteration will open it again.
		if err != nil {
			connection.Close()
			continue
		}

		// There was no RPC error. Now there can be two cases. Either the server
		// we talked to was the master, and it processes the request, or it was
		// not the master, in which case it tells us who the master is (if it
		// knows). The indicator for this is the presence of the mastership
		// field in the response.
		mastership := out.GetMastership()

		// If there was no mastership field in the response the server we talked
		// to was the master and has processed the request. If that is the case
		// we can return the response.
		if mastership == nil {
			return out, nil
		}

		// If there was a mastership message we check it for presence of the
		// master_bns field. If there is none then the server does not know
		// who the master is. In that case we need to retry.
		if mastership.MasterAddress == "" {
			log.Warningf("%v is not the master, and does not know who the master is", connection.currentMaster)
			continue
		}

		newMaster := mastership.MasterAddress

		// This should not happen, because if the server does not know who the master is
		// it should signify that through the absence of the master_bns field, but why
		// not check it.
		if newMaster == "" {
			log.Errorf("Unexpected error: %v", connection.currentMaster)
			continue
		}

		// The server we talked to told us who the master is. Connect to it.
		connection.connect(newMaster)

		goto RetryNoSleep
	}

	log.Error("runMasterAware failed to complete")

	return nil, err
}

// Close closes the connection of the client to the server.
func (connection *Connection) Close() {
	// Closes the current connection if there is one.
	if connection.conn != nil {
		log.Infof("closing the connection to %v", connection.currentMaster)
		connection.conn.Close()
		connection.conn = nil
		connection.currentMaster = ""
	}
}
