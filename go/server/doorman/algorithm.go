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
	"container/list"
	"time"

	pb "github.com/youtube/doorman/proto/doorman"
)

// epsilon is the smallest amount of capacity that the algorithms need
// to worry about. An algorithm might short circuit its processing if
// there is less than epislon to divide or give out. This value exists
// for optimisation purposes but also to prevent certain pathological
// cases because of imprecision in float64 math.
const epsilon = 1e-6

// Request specifies a requested capacity lease that an Algorithm may
// grant.
type Request struct {
	// Store is a lease store that an algorithm will operate on.
	Store LeaseStore

	// Client is the identifier of a client requesting capacity.
	Client string

	// Capacity is the current total capacity available for some
	// resource.
	Capacity float64

	// Has is the capacity the client claims it has been assigned
	// previously.
	Has float64

	// Wants is the capacity that the client desires.
	Wants float64

	// Subclients is the number of subclients that the client has.
	Subclients int64
}

// Algorithm is a function that takes a Request and returns the lease.
type Algorithm func(request Request) Lease

func getAlgorithmParams(config *pb.Algorithm) (leaseLength, refreshInterval time.Duration) {
	return time.Duration(config.GetLeaseLength()) * time.Second, time.Duration(config.GetRefreshInterval()) * time.Second
}

func minF(left, right float64) float64 {
	if left > right {
		return right
	}
	return left
}

func maxF(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

// NoAlgorithm returns the zero algorithm: every clients gets as much
// capacity as it asks for.
func NoAlgorithm(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(r Request) Lease {
		return r.Store.Assign(r.Client, length, interval, r.Wants, r.Wants, r.Subclients)
	}
}

// Static assigns to each client the same configured capacity. Note
// that this algorithm attaches a different meaning to config.Capacity
// - it is the per client assigned capacity, not the total capacity
// available to all clients.
func Static(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(r Request) Lease {
		return r.Store.Assign(r.Client, length, interval, minF(r.Capacity, r.Wants), r.Wants, r.Subclients)
	}
}

// FairShare assigns to each client a "fair" share of the available
// capacity. The definition of "fair" is as follows: In case the total
// wants is below the total available capacity, everyone gets what they want.
// If however the total wants is higher than the total available capacity
// everyone is guaranteed their equal share of the capacity. Any capacity
// left by clients who ask for less than their equal share is distributed
// to clients who ask for more than their equal share in a way so that
// available capacity is partitioned in equal parts in an iterative way.
func FairShare(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(r Request) Lease {
		var (
			count = r.Store.Count()
			old   = r.Store.Get(r.Client)
		)

		// If this is a new client, we adjust the count.
		if !r.Store.HasClient(r.Client) {
			count += r.Subclients
		}

		// This is the equal share that every subclient should get.
		equalShare := r.Capacity / float64(count)

		// This is the equal share that should be assigned to the current client
		// based on the number of its subclients.
		equalSharePerClient := equalShare * float64(r.Subclients)

		// This is the capacity which is currently unused (assuming that
		// the requesting client has no capacity). It is the maximum
		// capacity that this run of the algorithm can assign to the
		// requesting client.
		unusedCapacity := r.Capacity - r.Store.SumHas() + old.Has

		// If the client wants less than its equal share or
		// if the sum of what all clients want together is less
		// than the available capacity we can give this client what
		// it wants.
		if r.Wants <= equalSharePerClient || r.Store.SumWants() <= r.Capacity {
			return r.Store.Assign(r.Client, length, interval,
				minF(r.Wants, unusedCapacity), r.Wants, r.Subclients)
		}

		// Map of what each client will get from this algorithm run.
		// Ultimately we need only determine what this client gets,
		// but for the algorithm we need to keep track of what
		// other clients would be getting as well.
		gets := make(map[string]float64)

		// This list contains all the clients which are still in
		// the race to get some capacity.
		clients := list.New()
		clientsNext := list.New()

		// Puts every client in the list to signify that they all
		// still need capacity.
		r.Store.Map(func(id string, lease Lease) {
			clients.PushBack(id)
		})

		// This is the capacity that can still be divided.
		availableCapacity := r.Capacity

		// The clients list now contains all the clients who still need/want
		// capacity. We are going to divide the capacity in availableCapacity
		// in iterations until there is nothing more to divide or until we
		// completely satisfied the client who is making the request.
		for availableCapacity > epsilon && gets[r.Client] < r.Wants-epsilon {
			// Calculate number of subclients for the given clients list.
			clientsCopy := *clients
			var subclients int64
			for e := clientsCopy.Front(); e != nil; e = e.Next() {
				subclients += r.Store.Subclients(e.Value.(string))
			}

			// This is the fair share that is available to all subclients
			// who are still in the race to get capacity.
			fairShare := availableCapacity / float64(subclients)

			// Not enough capacity to actually worry about. Prevents
			// an infinite loop.
			if fairShare < epsilon {
				break
			}

			// We now process the list of clients and give a topup to
			// every client's capacity. That topup is either the fair
			// share or the capacity still needed to be completely
			// satisfied (in case the client needs less than the
			// fair share to reach its need/wants).
			for e := clients.Front(); e != nil; e = e.Next() {
				id := e.Value.(string)

				// Determines the wants for this client, which comes
				// either from the store, or from the request info if it
				// is the client making the request,
				var wants float64
				var subclients int64

				if id == r.Client {
					wants = r.Wants
					subclients = r.Subclients
				} else {
					wants = r.Store.Get(id).Wants
					subclients = r.Store.Subclients(id)
				}

				// stillNeeds is the capacity this client still needs to
				// be completely satisfied.
				stillNeeds := wants - gets[id]

				// Tops up the client's capacity.
				// Every client should receive the resource capacity based on
				// the number of its subclients.
				fairSharePerClient := fairShare * float64(subclients)
				if stillNeeds <= fairSharePerClient {
					gets[id] += stillNeeds
					availableCapacity -= stillNeeds
				} else {
					gets[id] += fairSharePerClient
					availableCapacity -= fairSharePerClient
				}

				// If the client is not yet satisfied we include it in the next
				// generation of the list. If it is completely satisfied we
				// don't.
				if gets[id] < wants {
					clientsNext.PushBack(id)
				}
			}

			// Ready for the next iteration of the loop. Swap the clients with the
			// clientsNext list.
			clients, clientsNext = clientsNext, clients

			// Cleans the clientsNext list so that it can be used again.
			clientsNext.Init()
		}

		// Insert the capacity grant into the lease store. We cannot
		// give out more than the currently unused capacity. If that
		// is less than what the algorithm calculated this will get
		// fixed in the next capacity refreshes.
		return r.Store.Assign(r.Client, length, interval,
			minF(gets[r.Client], unusedCapacity), r.Wants, r.Subclients)
	}
}

// ProportionalShare assigns to each client what it wants, except
// in an overload situation where each client gets at least an equal
// share (if it wants it), plus (if possible) a share of the capacity
// left on the table by clients who are requesting less than their
// equal share proportional to what the client is asking for.
func ProportionalShare(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(r Request) Lease {
		var (
			count = r.Store.Count()
			old   = r.Store.Get(r.Client)
			gets  = 0.0
		)

		// If this is a new client, we adjust the count.
		if !r.Store.HasClient(r.Client) {
			count += r.Subclients
		}

		// This is the equal share that every subclient has an absolute
		// claim on.
		equalShare := r.Capacity / float64(count)

		// This is the equal share that should be assigned to the current client
		// based on the number of its subclients.
		equalSharePerClient := equalShare * float64(r.Subclients)

		// This is the capacity which is currently unused (assuming that
		// the requesting client has no capacity). It is the maximum
		// capacity that this run of the algorithm can assign to the
		// requesting client.
		unusedCapacity := r.Capacity - r.Store.SumHas() + old.Has

		// If the client wants less than it equal share or
		// if the sum of what all clients want together is less
		// than the available capacity we can give this client what
		// it wants.
		if r.Store.SumWants() <= r.Capacity || r.Wants <= equalSharePerClient {
			return r.Store.Assign(r.Client, length, interval,
				minF(r.Wants, unusedCapacity), r.Wants, r.Subclients)
		}

		// We now need to determine if we can give a top-up on
		// the equal share. The capacity for this top up comes
		// from clients who want less than their equal share,
		// so we calculate how much capacity this is. We also
		// calculate the excess need of all the clients, which
		// is the sum of all the wants over the equal share.
		extraCapacity := 0.0
		extraNeed := 0.0

		r.Store.Map(func(id string, lease Lease) {
			var wants float64
			var subclients int64

			if id == r.Client {
				wants = r.Wants
				subclients = r.Subclients
			} else {
				wants = lease.Wants
				subclients = lease.Subclients
			}

			// Every client should receive the resource capacity based on the number
			// of subclients it has.
			equalSharePerClient := equalShare * float64(subclients)
			if wants < equalSharePerClient {
				extraCapacity += equalSharePerClient - wants
			} else {
				extraNeed += wants - equalSharePerClient
			}
		})

		// Every client with a need over the equal share will get
		// a proportional top-up.
		gets = equalSharePerClient + (r.Wants-equalSharePerClient)*(extraCapacity/extraNeed)

		// Insert the capacity grant into the lease store. We cannot
		// give out more than the currently unused capacity. If that
		// is less than what the algorithm calculated we will
		// adjust this in the next capacity refreshes.

		return r.Store.Assign(r.Client, length, interval,
			minF(gets, unusedCapacity), r.Wants, r.Subclients)
	}
}

// Learn returns the algorithm used in learning mode. It assigns to
// the client the same capacity it reports it had before.
func Learn(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)
	return func(r Request) Lease {
		return r.Store.Assign(r.Client, length, interval, r.Has, r.Wants, r.Subclients)
	}
}

var algorithms = map[pb.Algorithm_Kind]func(*pb.Algorithm) Algorithm{
	pb.Algorithm_NO_ALGORITHM:       NoAlgorithm,
	pb.Algorithm_STATIC:             Static,
	pb.Algorithm_PROPORTIONAL_SHARE: ProportionalShare,
	pb.Algorithm_FAIR_SHARE:         FairShare,
}

func GetAlgorithm(config *pb.Algorithm) Algorithm {
	return algorithms[config.GetKind()](config)
}
