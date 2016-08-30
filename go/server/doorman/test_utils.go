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
	"fmt"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"doorman/go/connection"
	"doorman/go/server/election"

	pb "doorman/proto/doorman"
)

// MakeTestServer creates a test root server. It doesn't start any
// goroutines necessary to handle master election. It is supposed to
// be used only in tests. On top of making the test server it also
// unit tests some functions related to server configuration.
func MakeTestServer(resources ...*pb.ResourceTemplate) (*Server, error) {
	return MakeTestIntermediateServer("test", "", resources...)
}

// MakeTestIntermediateServer creates a test intermediate server with
// specified name and connected to the lower-level server with address addr.
func MakeTestIntermediateServer(name string, addr string, resources ...*pb.ResourceTemplate) (*Server, error) {
	// Creates a new test server that is the master.
	server, err := NewIntermediate(context.Background(), name, addr, election.Trivial(), connection.MinimumRefreshInterval(0), connection.DialOpts(grpc.WithInsecure()))
	if err != nil {
		return nil, fmt.Errorf("server.NewIntermediate: %v", err)
	}

	// If this is a root server, then it should not be configured until we explicitly call LoadConfig
	// with the initial resources configuration. For intermediate server it is not the case.
	if addr == "" {
		if err := server.LoadConfig(context.Background(), &pb.ResourceRepository{
			Resources: resources,
		}, map[string]*time.Time{}); err != nil {
			return nil, fmt.Errorf("server.LoadConfig: %v", err)
		}
	}

	// Waits until the server is configured. This should not block and immediately fall through.
	server.WaitUntilConfigured()

	return server, nil
}
