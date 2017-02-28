// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package concurrency

import (
	"fmt"
	"math"

	v3 "github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/mvcc/mvccpb"
	"golang.org/x/net/context"
)

func waitDelete(ctx context.Context, client *v3.Client, key string, rev int64) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wch := client.Watch(cctx, key, v3.WithRev(rev))
	for wr := range wch {
		for _, ev := range wr.Events {
			if ev.Type == mvccpb.DELETE {
				return nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("lost watcher waiting for delete")
}

// waitDeletes efficiently waits until all keys matched by Get(key, opts...) are deleted
func waitDeletes(ctx context.Context, client *v3.Client, key string, opts ...v3.OpOption) error {
	getOpts := []v3.OpOption{v3.WithSort(v3.SortByCreateRevision, v3.SortAscend)}
	getOpts = append(getOpts, opts...)
	resp, err := client.Get(ctx, key, getOpts...)
	maxRev := int64(math.MaxInt64)
	getOpts = append(getOpts, v3.WithRev(0))
	for err == nil {
		for len(resp.Kvs) > 0 {
			i := len(resp.Kvs) - 1
			if resp.Kvs[i].CreateRevision <= maxRev {
				break
			}
			resp.Kvs = resp.Kvs[:i]
		}
		if len(resp.Kvs) == 0 {
			break
		}
		lastKV := resp.Kvs[len(resp.Kvs)-1]
		maxRev = lastKV.CreateRevision
		err = waitDelete(ctx, client, string(lastKV.Key), maxRev)
		if err != nil || len(resp.Kvs) == 1 {
			break
		}
		getOpts = append(getOpts, v3.WithLimit(int64(len(resp.Kvs)-1)))
		resp, err = client.Get(ctx, key, getOpts...)
	}
	return err
}
