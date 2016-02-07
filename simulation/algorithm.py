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

import math

from config_wrapper import global_config
from lease_pb2 import CapacityLease
from utils import clock, logger

# Default values for the lease duration, decay factor and refresh
# interval.
_kDefaultLeaseDuration = 60
_kDefaultDecayFactor = 0.5
_kDefaultRefreshInterval = 16


# Abstract base class for algorithm classes.
class AlgorithmImpl(object):

  # Factory method. Creates a specifica algorithm object
  # for a resource (based on its template). The algorithm
  # needs to know the server_level to calculate the refresh
  # intervals correctly.

  @classmethod
  def create(self, template, server_level):
    # If the template specifies the algorithm, that's the one
    # we will use. If not there might be a default algorithm
    # specified in the configuration. If that is not available
    # we default to the "None" algorithm.
    if template.HasField('algorithm'):
      algo = template.algorithm
    else:
      algo = global_config.get_default_algorithm()

    if algo.name == 'Static':
      from algo_static import StaticAlgorithm
      a = StaticAlgorithm(algo, server_level)
    elif algo.name == 'None':
      from algo_none import NoneAlgorithm
      a = NoneAlgorithm(algo, server_level)
    elif algo.name == 'ProportionalShare':
      from algo_proportional import ProportionalShareAlgorithm
      a = ProportionalShareAlgorithm(algo, server_level)
    else:
      logger.warning('Unknown algorithm: %s' % algo.name)
      a = NoneAlgorithm(algo, server_level)

    a.server_level = server_level

    return a

  # Gets a named algorithm parameter from the configuration. The
  # algorithm objects use this to get their configuration data.
  # Note: The returned value is always a string.
  def get_named_parameter(self, algo, name, default=None):
    for p in algo.param:
      if p.name == name:
        return p.value

    return default

  # Gets the standard parameters that many (well, at least
  # more than one) algorithms need, and stores them into the
  # algorithm object.
  def _get_default_parameters(self, algo):
    self.lease_duration_secs = int(
        self.get_named_parameter(
            algo, 'lease_duration_secs',
            _kDefaultLeaseDuration))
    self.decay_factor = float(
        self.get_named_parameter(
            algo, 'decay_factor',
            _kDefaultDecayFactor))
    self.refresh_interval = int(
        self.get_named_parameter(
            algo, 'refresh_interval',
            _kDefaultRefreshInterval))

  # Returns the refresh interval for this server, based on the
  # refresh_interval and the level of the server. The
  # refresh interval stored in the configuration is the interval
  # that the root server gives out, and then every level above
  # that halves that interval.
  def get_refresh_interval(self):
    return int(
        math.pow(self.decay_factor, self.server_level) *
        self.refresh_interval)

  # Returns the maximum lease duration this algorithm will ever
  # give out. The server needs to know that to determine the
  # time that a resources will be in learning mode.
  def get_max_lease_duration(self):
    return self.lease_duration_secs

  # Creates a new lease for a certain capacity for a given resource.
  def create_lease(self, resource, capacity):
    now = clock.get_time()

    # Creates the lease object and sets the essential properties.
    lease = CapacityLease()
    lease.capacity = capacity
    lease.refresh_interval = self.get_refresh_interval()

    # The server cannot give out a lease that lasts longer than
    # it itself has capacity for from a lower level.
    if resource.HasField('has'):
      lease.expiry_time = min(
          resource.has.expiry_time, now + self.lease_duration_secs)
    else:
      lease.expiry_time = now + self.lease_duration_secs

    # Edge case: If we are near the end of the lease we must make sure
    # that the refresh interval does not tell the client to do a refresh
    # after the resource has expired. If that happens, adjust the
    # refresh interval to just before the end of the lease expiration.
    if now + lease.refresh_interval >= lease.expiry_time:
      lease.refresh_interval = lease.expiry_time - now - 1

    assert lease.IsInitialized()

    return lease
