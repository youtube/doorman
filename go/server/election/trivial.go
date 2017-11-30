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
