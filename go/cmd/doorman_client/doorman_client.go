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

// Command line client for Doorman.
//
// Usage:
// doorman_client --server=localhost:9999 --resource=test_resource --wants=100 --client_id=foo

package main

import (
	"flag"
	"fmt"

	log "github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"doorman/go/client/doorman"
)

var (
	server   = flag.String("server", "", "Address of the doorman server")
	resource = flag.String("resource", "", "Name of the resource to request capacity for")
	wants    = flag.Float64("wants", 0, "Amount of capacity to request")
	clientID = flag.String("client_id", "", "Client id to use")
	caFile   = flag.String("ca_file", "", "The file containning the CA root cert file to connect over TLS (otherwise plain TCP will be used)")
)

func main() {
	flag.Parse()

	if *server == "" || *resource == "" {
		log.Exit("both --server and --resource must be specified")
	}

	if *clientID == "" {
		log.Exit("--client_id must be set")
	}

	var opts []grpc.DialOption
	if len(*caFile) != 0 {
		var creds credentials.TransportCredentials
		var err error
		creds, err = credentials.NewClientTLSFromFile(*caFile, "")
		if err != nil {
			log.Exitf("Failed to create TLS credentials %v", err)
		}

		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	client, err := doorman.NewWithID(*server, *clientID, doorman.DialOpts(opts...))

	if err != nil {
		log.Exitf("could not create client: %v", err)
	}

	defer client.Close()
	resource, err := client.Resource(*resource, *wants)

	if err != nil {
		log.Exitf("could not acquire resource: %v", err)
	}

	fmt.Println(<-resource.Capacity())
}
