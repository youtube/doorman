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

package election

import (
	"fmt"
	"github.com/coreos/etcd/client"
	log "github.com/golang/glog"
	"golang.org/x/net/context"
	"time"
)

type etcdElection struct {
	endpoints []string
	client    client.KeysAPI

	lease    time.Duration
	lock     string
	isMaster chan bool
	current  chan string
}

var _ Election = &etcdElection{}

// NewEtcdElection returns an etcd based master election (endpoints are used to
// connect to the etcd cluster). The participants synchronize on lock,
// and the master has delay time to renew its lease. Higher values of
// delay may lead to more stable mastership at the cost of potentially
// longer periods without any master.
func NewEtcdElection(endpoints []string, lock string, lease time.Duration) Election {
	return &etcdElection{
		endpoints: endpoints,
		isMaster:  make(chan bool),
		current:   make(chan string),
		lease:     lease,
		lock:      lock,
	}
}

func (e *etcdElection) String() string {
	return fmt.Sprintf("etcd lock: %v (endpoints: %v)", e.lock, e.endpoints)
}

func (e *etcdElection) Current() chan string {
	return e.current
}

func (e *etcdElection) IsMaster() chan bool {
	return e.isMaster
}

func (e *etcdElection) Run(ctx context.Context, id string) error {
	err := e.init()
	if err != nil {
		return err
	}

	go e.refreshMastership(ctx)
	go e.campaignAndRenew(ctx, id)
	return nil
}

//
func (e *etcdElection) init() error {
	c, err := client.New(client.Config{Endpoints: e.endpoints})
	if err != nil {
		return err
	}
	log.V(2).Infof("connected to etcd at %v", e.endpoints)
	e.client = client.NewKeysAPI(c)
	return nil
}

//
func (e *etcdElection) refreshMastership(ctx context.Context) {
	w := e.client.Watcher(e.lock, nil)
	log.V(2).Infof("watching %v for master updates", e.lock)
	var last string
	for {
		r, err := w.Next(ctx)
		if err != nil {
			if !client.IsKeyNotFound(err) {
				log.Errorf("Failed receiving new master: %v", err)
			}
			e.current <- ""
			time.Sleep(e.lease)
			continue
		}
		log.V(2).Infof("received master update: %v", r)
		if last != r.Node.Value {
			last = r.Node.Value
			e.current <- r.Node.Value
		}
	}
}

//
func (e *etcdElection) campaignAndRenew(ctx context.Context, id string) {
	for {
		log.V(2).Infof("trying to become master at %v", e.lock)
		if _, err := e.client.Set(ctx, e.lock, id, &client.SetOptions{
			TTL:       e.lease,
			PrevExist: client.PrevNoExist,
		}); err != nil {
			log.V(2).Infof("failed becoming the master, retrying in %v: %v", e.lease, err)
			time.Sleep(e.lease)
			continue
		}
		e.isMaster <- true
		log.V(2).Info("Became master at %v as %v.", e.lock, id)
		for {
			time.Sleep(e.lease / 5)
			log.V(2).Infof("Renewing mastership lease at %v as %v", e.lock, id)
			var err error
			for retry := 0; retry < 3; retry++ {
				_, err = e.client.Set(ctx, e.lock, id, &client.SetOptions{
					TTL:       e.lease,
					PrevExist: client.PrevExist,
					PrevValue: id,
				})

				if err != nil {
					time.Sleep(e.lease / 5)
				} else {
					break
				}
			}

			if err != nil {
				log.V(2).Info("lost mastership")
				e.isMaster <- false
				break
			}
		}
	}
}
