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

// Package election implements master elections. Currently only etcd
// is supported.
package election

import (
	"fmt"
	"time"

	"github.com/coreos/etcd/client"
	log "github.com/golang/glog"
	"golang.org/x/net/context"
)

// Election represents a master election.
type Election interface {
	// Run enters into the election using id as the identifier.
	Run(ctx context.Context, id string) error
	// IsMaster returns a channel on which the status of the
	// election will be broadcasted (true means that this is the
	// master).
	IsMaster() chan bool
	// Current returns a channel on which the name of the current
	// master is broadcasted. The empty string means that the
	// master is currently unknown.
	Current() chan string
}

type trivial struct {
	isMaster chan bool
	current  chan string
}

// Trivial returns a trivial election: the participant immediately
// wins. Use this if you need the election interface, but don't really
// care about the master election (eg. you'll never have more than one
// candidate).
func Trivial() Election {
	return &trivial{
		isMaster: make(chan bool, 1),
		current:  make(chan string, 1),
	}
}

func (e *trivial) String() string {
	return "no election, acting as the master"
}

func (e *trivial) Current() chan string {
	return e.current
}

func (e *trivial) IsMaster() chan bool {
	return e.isMaster
}

func (e *trivial) Run(_ context.Context, id string) error {
	e.isMaster <- true
	e.current <- id
	return nil
}

type etcd struct {
	endpoints []string
	delay     time.Duration
	isMaster  chan bool
	lock      string
	current   chan string
}

// Etcd returns an etcd based master election (endpoints are used to
// connect to the etcd cluster). The participants synchronize on lock,
// and the master has delay time to renew its lease. Higher values of
// delay may lead to more stable mastership at the cost of potentially
// longer periods without any master.
func Etcd(endpoints []string, lock string, delay time.Duration) Election {
	return &etcd{
		endpoints: endpoints,
		isMaster:  make(chan bool),
		current:   make(chan string),
		delay:     delay,
		lock:      lock,
	}
}

func (e *etcd) String() string {
	return fmt.Sprintf("etcd lock: %v (endpoints: %v)", e.lock, e.endpoints)
}

func (e *etcd) Current() chan string {
	return e.current
}

func (e *etcd) IsMaster() chan bool {
	return e.isMaster
}

func (e *etcd) Run(ctx context.Context, id string) error {
	c, err := client.New(client.Config{Endpoints: e.endpoints})
	if err != nil {
		return err
	}
	log.V(2).Infof("connected to etcd at %v", e.endpoints)
	kapi := client.NewKeysAPI(c)

	go func() {
		w := kapi.Watcher(e.lock, nil)
		log.V(2).Infof("watching %v for master updates", e.lock)
		var last string
		for {
			r, err := w.Next(ctx)
			if err != nil {
				if !client.IsKeyNotFound(err) {
					log.Errorf("Failed receiving new master: %v", err)
				}
				e.current <- ""
				time.Sleep(e.delay)
				continue
			}
			log.V(2).Infof("received master update: %v", r)
			if last != r.Node.Value {
				last = r.Node.Value
				e.current <- r.Node.Value
			}
		}
	}()

	go func() {
		for {
			log.V(2).Infof("trying to become master at %v", e.lock)
			if _, err := kapi.Set(ctx, e.lock, id, &client.SetOptions{
				TTL:       e.delay,
				PrevExist: client.PrevNoExist,
			}); err != nil {
				log.V(2).Infof("failed becoming the master, retrying in %v: %v", e.delay, err)
				time.Sleep(e.delay)
				continue
			}
			e.isMaster <- true
			log.V(2).Info("Became master at %v as %v.", e.lock, id)
			for {
				time.Sleep(e.delay / 3)
				log.V(2).Infof("Renewing mastership lease at %v as %v", e.lock, id)
				_, err := kapi.Set(ctx, e.lock, id, &client.SetOptions{
					TTL:       e.delay,
					PrevExist: client.PrevExist,
					PrevValue: id,
				})

				if err != nil {
					log.V(2).Info("lost mastership")
					e.isMaster <- false
					break
				}
			}
		}
	}()
	return nil
}
