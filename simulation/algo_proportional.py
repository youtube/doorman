# Copyright 2016 Google, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from algorithm import AlgorithmImpl
from state_utils import *
from utils import logger


# Implements the "ProportionalShare" algorithm, where every client gets
# what it wants, except when the sum of wants is larger than the available
# capacity, in which case everybody gets a proportion of their request.
# A potential problem is when one client asks for a million billion,
# which means that it will get all capacity and all the other clients get
# next to nothing. The FairShare algorithm solves this.
class ProportionalShareAlgorithm(AlgorithmImpl):

  def __init__(self, algo, server_level):
    self._get_default_parameters(algo)

  def run_algo(self, resource, rr, this_wants):
    # The current capacity of the client on behalf of which the algorithm
    # is running does not count for determining the outstanding capacity
    rr.ClearField('has')

    # Calculates the sum of what everyone wants from this server.
    all_wants = sum_wants(resource)

    # Determines the capacity we have now.
    if resource.HasField('has'):
      has = resource.has.capacity
    else:
      # This can happen when the server was not able to get capacity
      # from a downstream server.
      has = 0

    # Calculates the free capacity.
    free_capacity = max(has - sum_leases(resource), 0)

    # If the sum of what all clients want together is less than the
    # available capacity we can probably just give this client what it
    # wants. However, we can never give more than the free capacity.
    if all_wants < has:
      rr.has.CopyFrom(
          self.create_lease(resource, min(this_wants, free_capacity)))
      return

    # We are in an overload situation. Every client can get only a
    # fraction of what it wants. Again we cannot hand more than the
    # free capacity.
    proportion = resource.has.capacity / all_wants
    rr.has.CopyFrom(
        self.create_lease(
            resource,
            min(this_wants * proportion, free_capacity)))

  # Runs the algorithm for a client.
  def run_client(self, resource, cr):
    self.run_algo(resource, cr, cr.wants)

  # Runs the algorithm for a server.
  def run_server(self, resource, sr):
    self.run_algo(resource, sr, sum([w.wants for w in sr.wants]))
