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

// doorman_shell allows you to emulate clients using a Doorman service
// by requesting and releasing capacity for resources for testing
// purposes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"

	"doorman/go/client/doorman"
	"github.com/chzyer/readline"
	log "github.com/golang/glog"
	"github.com/google/shlex"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	port   = flag.Int("port", 0, "port to bind to")
	server = flag.String("server", "", "Address of the doorman server")
	caFile = flag.String("ca_file", "", "The file containning the CA root cert file to connect over TLS (otherwise plain TCP will be used)")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, help)
	}
}

var help = `

This program allows you to interact with a Doorman service. A
successfull command outputs nothing, a failing command will output the
error.

Supported commands:

get CLIENT RESOURCE CAPACITY

  Request CAPACITY of RESOURCE for client. Requesting CAPACITY doesn't
  guarantee receiving it. Call "show" to see the current assingments.

release CLIENT RESOURCE
  
  Release any capacity for RESOURCE held by client.

show

  Show assignments for known client/resource pairs. Note that due to
  the asynchronous nature of the Doorman API you may not see the
  requested assignments immediately.

master

  Shows the current Doorman master for each client.

help
  
  Show this information.
`

// Multiclient wraps Doorman clients with various IDs and any
// requested resources.
type Multiclient struct {
	// addr is the address of the doorman service.
	addr string
	// opts is used when creating a client.
	opts []doorman.Option

	// clients is a cache of known clients.
	clients map[string]*doorman.Client

	// mu guards access to resources and capacities.
	mu sync.RWMutex
	// resources is a resource cache.
	resources map[key]doorman.Resource
	// capacities is the the currently known capacities.
	capacities map[key]float64
}

type key struct {
	client   string
	resource string
}

type assignment struct {
	key
	capacity float64
}

type assignments []assignment

func (as assignments) Len() int      { return len(as) }
func (as assignments) Swap(i, j int) { as[i], as[j] = as[j], as[i] }

func (as assignments) Less(i, j int) bool {
	if as[i].resource == as[j].resource {
		return as[i].client < as[j].client
	}
	return as[i].resource < as[j].resource
}

func (client *Multiclient) getClient(id string) (*doorman.Client, error) {
	c, ok := client.clients[id]
	if !ok {
		var err error
		c, err = doorman.NewWithID(client.addr, id, client.opts...)
		if err != nil {
			return nil, err
		}
		client.clients[id] = c
	}
	return c, nil
}

func (client *Multiclient) pollResource(k key, capacities chan float64) {
	for c := range capacities {
		client.mu.Lock()
		client.capacities[k] = c
		log.Infof("Received new capacity for %v: %v", k, c)
		client.mu.Unlock()
	}
	client.mu.Lock()
	delete(client.capacities, k)
	delete(client.resources, k)
	client.mu.Unlock()
}

func (client *Multiclient) getResource(clientID, resourceID string) (doorman.Resource, error) {
	k := key{clientID, resourceID}
	client.mu.RLock()
	r, ok := client.resources[k]
	client.mu.RUnlock()
	if ok {
		return r, nil
	}
	c, err := client.getClient(clientID)
	if err != nil {
		return nil, err
	}
	r, err = c.Resource(resourceID, 0.0)
	if err != nil {
		return nil, err
	}
	client.mu.Lock()
	client.resources[k] = r
	client.mu.Unlock()
	go client.pollResource(k, r.Capacity())
	return r, nil
}

func (client *Multiclient) Get(id string, resource string, wants float64) error {
	r, err := client.getResource(id, resource)
	if err != nil {
		return err
	}
	return r.Ask(wants)
}

func (client *Multiclient) Release(id string, resource string) error {
	r, err := client.getResource(id, resource)
	if err != nil {
		return err
	}
	return r.Release()
}

func (client *Multiclient) Eval(command []string) error {
	if len(command) == 0 {
		return nil
	}
	head, tail := command[0], command[1:]

	switch head {
	case "get":
		if len(tail) != 3 {
			return errors.New(`syntax is: get CLIENT RESOURCE CAPACITY"`)
		}
		clientID, resourceID, capString := tail[0], tail[1], tail[2]
		capacity, err := strconv.ParseFloat(capString, 64)
		if err != nil {
			return err
		}
		return client.Get(clientID, resourceID, capacity)
	case "show":
		if len(tail) != 0 {
			return errors.New("show takes no arguments")
		}
		capacities := make(assignments, 0)
		client.mu.RLock()
		for k, v := range client.capacities {
			capacities = append(capacities, assignment{key: k, capacity: v})
		}
		client.mu.RUnlock()
		sort.Sort(capacities)
		for _, p := range capacities {
			fmt.Printf("client: %q\nresource: %q\ncapacity: %v\n\n", p.client, p.resource, p.capacity)
		}
		return nil
	case "release":
		if len(tail) != 2 {
			return errors.New(`syntax is "release CLIENT RESOURCE`)
		}
		clientID, resourceID := tail[0], tail[1]
		return client.Release(clientID, resourceID)
	case "master":
		for k, v := range client.clients {
			fmt.Printf("%s: %s\n", k, v.GetMaster())
		}
		return nil
	case "help":
		fmt.Fprintln(os.Stderr, help)
		return nil
	case "quit", "q", "bye":
		return io.EOF
	default:
		return errors.New("unrecognized command")
	}
}

func (client *Multiclient) Close() {
	client.mu.Lock()
	for _, r := range client.resources {
		if err := r.Release(); err != nil {
			log.Errorf("error during release: %v\n", err)
		}
	}
	client.mu.Unlock()
	for _, c := range client.clients {
		c.Close()
	}
}

func main() {
	flag.Parse()

	if *server == "" {
		fmt.Fprintf(os.Stderr, "--server cannot be empty.\n")
		os.Exit(1)
	}

	if *port != 0 {
		http.Handle("/metrics", prometheus.Handler())
		go http.ListenAndServe(fmt.Sprintf(":%v", *port), nil)
	}

	var opts []grpc.DialOption
	if len(*caFile) != 0 {
		var creds credentials.TransportCredentials
		var err error
		creds, err = credentials.NewClientTLSFromFile(*caFile, "")
		if err != nil {
			log.Exitf("Failed to create TLS credentials %v\n", err)
		}

		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	c := &Multiclient{
		addr:      *server,
		opts:      []doorman.Option{doorman.DialOpts(opts...)},
		clients:   make(map[string]*doorman.Client),
		resources: make(map[key]doorman.Resource),

		capacities: make(map[key]float64),
	}

	defer c.Close()

	line, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     "/tmp/doorman_shell.tmp",
		InterruptPrompt: "\nInterrupt, Press Ctrl+D to exit",
	})
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	for {
		data, err := line.Readline()
		if err == io.EOF {

			break
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		}

		command, err := shlex.Split(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		}

		err = c.Eval(command)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		}
	}
}
