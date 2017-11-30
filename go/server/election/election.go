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
