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
	"time"

	log "github.com/golang/glog"

	pb "doorman/proto/doorman"
)

// Request specifies a requested capacity lease that an Algorithm may
// grant.
type Request struct {
	// Client is the identifier of a client requesting capacity.
	Client string

	// Has is the capacity the client claims it has been assigned
	// previously.
	Has float64

	// Wants is the capacity that the client desires.
	Wants float64

	// Subclients is the number of subclients that the client has.
	Subclients int64
}

// Algorithm is a function that takes a LeaseStore, the total
// available capacity, and a Request, and returns the lease.
type Algorithm func(store LeaseStore, capacity float64, request Request) Lease

func getAlgorithmParams(config *pb.Algorithm) (leaseLength, refreshInterval time.Duration) {
	return time.Duration(config.LeaseLength) * time.Second, time.Duration(config.RefreshInterval) * time.Second
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

	return func(store LeaseStore, capacity float64, r Request) Lease {
		return store.Assign(r.Client, length, interval, r.Wants, r.Wants, r.Subclients)
	}
}

// Static assigns to each client the same configured capacity. Note
// that this algorithm attaches a different meaning to config.Capacity
// - it is the per client assigned capacity, not the total capacity
// available to all clients.
func Static(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(store LeaseStore, capacity float64, r Request) Lease {
		return store.Assign(r.Client, length, interval, minF(capacity, r.Wants), r.Wants, r.Subclients)
	}
}

// FairShare assigns to each client a "fair" share of the available
// capacity. The definition of "fair" is as follows: In case the total
// wants is below the total available capacity, everyone gets what
// they want.  If however the total wants is higher than the total
// available capacity everyone is guaranteed their equal share of the
// capacity. Any capacity left by clients who ask for less than their
// equal share is distributed to clients who ask for more than their
// equal share in a way so that available capacity is partitioned in
// equal parts in an iterative way.
func FairShare(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)

	return func(store LeaseStore, capacity float64, r Request) Lease {
		// Get the lease for this client in the store. If it's not
		// there, that's fine, we will get the zero lease (which works
		// for the calculations that come next).
		old := store.Get(r.Client)

		// A sanity check: what the client thinks it has should match
		// what the server thinks it has. This is not a problem for
		// now, as the server will depend on its own version, but it
		// may be a sign of problems with a client implementation, so it's better to log it.
		if r.Has != old.Has {
			log.Errorf("client %v is confused: says it has %v, was assigned %v.", r.Client, r.Has, old.Has)
		}

		// Find the number of subclients that we know of. Note that
		// the number of sublclients for this request's client may
		// have changed, so we have to take it in account.
		count := store.Count() - old.Subclients + r.Subclients

		// Let's find see how much capacity is actually available. We
		// can count into that the capacity the client has been
		// assigned.
		available := capacity - store.SumHas() + old.Has

		// Amount of capacity that each subclient is entitled to.
		equalShare := capacity / float64(count)

		// How much capacity this client's subclients are entitled to.
		deservedShare := equalShare * float64(r.Subclients)

		// If the client is asking for less than it deserves, that's
		// fine with us. We can just give it to it, assuming that
		// there's enough capacity available.
		if r.Wants <= deservedShare {
			return store.Assign(r.Client, length, interval, minF(r.Wants, available), r.Wants, r.Subclients)
		}

		// If we reached here, it means that the client wants more
		// than its fair share. We have to find out the extent to
		// which we can accomodate it.

		var (
			// First round of capacity redistribution. extra is the
			// capacity left by clients asking for less than their
			// fair share.
			extra float64

			// wantExtra is the number of subclients that are competing
			// for the extra capacity. We know that the current client is
			// there.
			wantExtra = r.Subclients

			// Save all the clients that want extra capacity. We'll
			// need this list for the second round of extra
			// distribution.
			wantExtraClients = make(map[string]Lease)
		)

		store.Map(func(id string, lease Lease) {
			if id == r.Client {
				return
			}
			deserved := float64(lease.Subclients) * equalShare
			if lease.Wants < deserved {
				// Wants less than it deserves. Put the unclaimed
				// capacity in the extra pool
				extra += deserved - lease.Wants
			} else if lease.Wants > deserved {
				// Wants more than it deserves. Add its clients to the
				// extra contenders.
				wantExtra += lease.Subclients
				wantExtraClients[id] = lease
			}
		})

		// deservedExtra is the chunk of the extra pool this client is
		// entitled to.
		deservedExtra := extra / float64(wantExtra) * float64(r.Subclients)

		// The client wants some extra, but less than to what it is
		// entitled.
		if r.Wants < deservedShare+deservedExtra {
			return store.Assign(r.Client, length, interval, minF(r.Wants, available), r.Wants, r.Subclients)
		}

		// Second round of extra capacity distribution: some clients
		// may have asked for more than their fair share, but less
		// than the chunk of extra that they are entitled to. Let's
		// redistribute that.

		var (
			wantExtraExtra = r.Subclients
			extraExtra     float64
		)
		for id, lease := range wantExtraClients {
			if id == r.Client {
				continue
			}

			if lease.Wants < deservedExtra+deservedShare {
				extraExtra += deservedExtra + deservedShare - lease.Wants
			} else if lease.Wants > deservedExtra+deservedShare {
				wantExtraExtra += lease.Subclients
			}
		}
		deservedExtraExtra := extraExtra / float64(wantExtraExtra) * float64(r.Subclients)
		return store.Assign(r.Client, length, interval, minF(deservedShare+deservedExtra+deservedExtraExtra, available), r.Wants, r.Subclients)
	}
}

// ProportionalShare assigns to each client what it wants, except
// in an overload situation where each client gets at least an equal
// share (if it wants it), plus (if possible) a share of the capacity
// left on the table by clients who are requesting less than their
// equal share proportional to what the client is asking for.
func ProportionalShare(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)
	return func(store LeaseStore, capacity float64, r Request) Lease {
		var (
			count = store.Count()
			old   = store.Get(r.Client)
			gets  = 0.0
		)

		// If this is a new client, we adjust the count.
		if !store.HasClient(r.Client) {
			count += r.Subclients
		}

		// This is the equal share that every subclient has an absolute
		// claim on.
		equalShare := capacity / float64(count)

		// This is the equal share that should be assigned to the current client
		// based on the number of its subclients.
		equalSharePerClient := equalShare * float64(r.Subclients)

		// This is the capacity which is currently unused (assuming that
		// the requesting client has no capacity). It is the maximum
		// capacity that this run of the algorithm can assign to the
		// requesting client.
		unusedCapacity := capacity - store.SumHas() + old.Has

		// If the client wants less than it equal share or
		// if the sum of what all clients want together is less
		// than the available capacity we can give this client what
		// it wants.
		if store.SumWants() <= capacity || r.Wants <= equalSharePerClient {
			return store.Assign(r.Client, length, interval,
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

		store.Map(func(id string, lease Lease) {
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

		return store.Assign(r.Client, length, interval,
			minF(gets, unusedCapacity), r.Wants, r.Subclients)
	}
}

// Learn returns the algorithm used in learning mode. It assigns to
// the client the same capacity it reports it had before.
func Learn(config *pb.Algorithm) Algorithm {
	length, interval := getAlgorithmParams(config)
	return func(store LeaseStore, capacity float64, r Request) Lease {
		return store.Assign(r.Client, length, interval, r.Has, r.Wants, r.Subclients)
	}
}

var algorithms = map[pb.Algorithm_Kind]func(*pb.Algorithm) Algorithm{
	pb.Algorithm_NO_ALGORITHM:       NoAlgorithm,
	pb.Algorithm_STATIC:             Static,
	pb.Algorithm_PROPORTIONAL_SHARE: ProportionalShare,
	pb.Algorithm_FAIR_SHARE:         FairShare,
}

func GetAlgorithm(config *pb.Algorithm) Algorithm {
	return algorithms[config.Kind](config)
}
