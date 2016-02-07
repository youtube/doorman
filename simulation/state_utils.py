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

# Utilities for the server and things that run on the server (like
# algorithms).

from utils import clock


# Helper function to determine if a resource is in learning mode.
def in_learning_mode(resource):
  return resource.learning_mode_expiry_time >= clock.get_time()


# Helper function to determine if a lease is expired.
# Note: If there is no lease, it is not expired!
def lease_expired(thing):
  return thing and thing.HasField('has') and thing.has.expiry_time <= clock.get_time()


# Calculates the sum of all wants by clients and servers that depend on this
# server.
def sum_wants(resource):
  # Calculates the sum of what all clients and higher level servers want.
  n = sum([client.wants for client in resource.client])

  for server in resource.server:
    for want in server.wants:
      n += want.wants

  return n


# Calculates the sum of capacity that this server has outstanding
# leases for.
def sum_leases(resource):
  return sum([
      client.has.capacity for client in resource.client
      if client.HasField('has')]) + sum([
          server.has.capacity
          for server in resource.server
          if server.HasField('has')])

# Calculates the sum of capacity that this server has outstanding
# responsibilities for. These responsibilities include all the
# clients that have leases, and all the outstanding responsibilities
# of the server. In a time of shrinking allocations a server might
# have more outstanding responsibilities than it has capacity to
# give out. We call this phenomenon 'shortfall'.
def sum_outstanding(resource):
  return sum([
      client.has.capacity for client in resource.client
      if client.HasField('has')]) + sum([
          server.outstanding
          for server in resource.server])
