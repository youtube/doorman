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

import sys

from algorithm import AlgorithmImpl
from config_wrapper import global_config
from protocol_pb2 import *
from scheduler import scheduler
from server_state_pb2 import *
from state_utils import *
from utils import clock, logger
from varz import Counter, Gauge


# A schedulable class that gives a log messages when a resource
# leaves learning mode
class _LeaveLearningMode(object):

  def execute(self, arg):
    logger.info('%s resource %s has left learning mode' % arg)


# A wrapper class around the server state. Contains operations to
# access and modify the state.
class ServerStateWrapper(object):

  def __init__(self, server):
    self.server = server
    self.wrapped_state = ServerState()
    self.wrapped_state.server_id = server.get_server_id()
    self.wrapped_state.server_level = server.get_server_level()
    self.reset()
    self.last_cleanup_time = -1

  def reset(self):
    self.wrapped_state.ClearField('election_victory_time')
    del self.wrapped_state.resource[:]
    self.assert_clean()

  # Asserts that the state is "clean" (meaning that it has been reset
  # and does not contain any data).
  def assert_clean(self):
    assert self.wrapped_state.IsInitialized()
    assert not self.wrapped_state.HasField('election_victory_time')

    if len(self.wrapped_state.resource) > 0:
      print self.wrapped_state

    assert len(self.wrapped_state.resource) == 0

  def is_initialized(self):
    return self.wrapped_state.IsInitialized()

  def get_server_id(self):
    return self.wrapped_state.server_id

  def get_server_level(self):
    return self.wrapped_state.server_level

  def get_election_victory_time(self):
    if self.wrapped_state.HasField('election_victory_time'):
      return self.wrapped_state.election_victory_time

    return None

  def set_election_victory_time(self):
    self.wrapped_state.election_victory_time = clock.get_time()

  # Returns the reporting data for this resource and server.
  def get_reporting_data(self, resource_id):
    from reporter import ReportingData

    for resource in self.wrapped_state.resource:
      if resource_id != resource.resource_id:
        continue

      # How much capacity does this server have for this resource?
      if resource.HasField('has'):
        has = resource.has.capacity
      else:
        has = 0

      data = ReportingData()
      data.wants = sum_wants(resource)
      data.has = has
      data.leases = sum_leases(resource)
      data.outstanding = sum_outstanding(resource)

      return data

    # Asking for reporting data for a resource that is not (yet)
    # int the configuration. This can happen if we are getting
    # resource data just after this server has become the master
    # and it has not seen any client calls for the resource yet.
    return None

  # This method implements the cleanup step of the GetCapacity and
  # GetServerCapacity RPCs. It goes through the server state and
  # removes resources, clients and servers that have expired
  # leases.
  def cleanup(self):
    now = clock.get_time()

    # No need to do cleanup if the last cleanup happened in the same
    # second.
    if self.last_cleanup_time == now:
      return
    else:
      self.last_cleanup_time = now
      logger.info('%s cleanup' % self.get_server_id())

    # This is the new resource list, pruned from all resources for
    # which the server does not have capacity.
    nrl = list()

    # First go through all the resources, and figure out which ones we
    # want to keep; these will be added to nrl.
    for r in self.wrapped_state.resource:
      # A resource that is still in learning mode might not have obtained
      # a lease yet but should not be cleaned. It is exempt from
      # cleaning.
      if in_learning_mode(r):
        logger.info('Not cleaning %s (in learning mode)' % r.resource_id)
        nrl.append(r)
      elif not lease_expired(r):
        # This resource is going to survive this cleanup step.
        nrl.append(r)

        # Go through the clients for this resource, and keep the ones
        # who still have an unexpired lease. They will be added to the
        # ncl.
        ncl = list()

        for c in r.client:
          if not lease_expired(c):
            ncl.append(c)
          else:
            logger.info(
                'Removing client %s from resource %s (lease expired)' %
                (c.client_id, r.resource_id))

        # Then go through the servers for this resource, and keep the
        # ones who still have an unexpired lease. They will be added
        # to the nsl.
        nsl = list()

        for s in r.server:
          if not lease_expired(s):
            nsl.append(s)
          else:
            logger.info(
                'Removing server %s from resource %s (lease expired)' %
                (s.server_id, r.resource_id))

        # Plug the new client and server list back into the resource.
        r.ClearField('client')
        r.client.extend(ncl)
        r.ClearField('server')
        r.server.extend(nsl)
      else:
        logger.info('Removing resource %s (lease expired)' % r.resource_id)

      # Now plug the new resource list back into the server state.
      self.wrapped_state.ClearField('resource')
      self.wrapped_state.resource.extend(nrl)

  # Finds a resource in the server state. If the resource does not exist
  # it tries to make a new blank resource. This only succeeds if we can
  # find a resource template in the configuration which matches the
  # resource. If the resource does not exist and cannot be made from an
  # existing template this method returns None.
  def find_resource(self, resource_id):
    # This can only happen in the master!
    assert self.server.is_master()

    # Lineair scan through all the resources in the state to find this
    # resource.
    for r in self.wrapped_state.resource:
      if r.resource_id == resource_id:
        return r

    # The resource was not found. Find the resource template which
    # describes this resource.
    t = global_config.find_resource_template(resource_id)

    # No template found.
    if not t:
      logger.error(
          'Cannot create server state entry for resource %s (no template)' %
          resource_id)
      return None

    # We got the template. Now create a new blank resource entry in the server
    # state.
    logger.info(
        '%s creating new resource %s' %
        (self.get_server_id(), resource_id))
    r = self.wrapped_state.resource.add()
    r.template.CopyFrom(t)
    r.resource_id = resource_id

    # Calculates the time this resource went out of learning mode.
    # Note: This time may be in the past.
    r.learning_mode_expiry_time = self.wrapped_state.election_victory_time + \
      AlgorithmImpl.create(t, self.wrapped_state.server_level).get_max_lease_duration()

    if r.learning_mode_expiry_time > clock.get_time():
      logger.info(
          '%s putting resource %s in learning mode until T=%d' %
          (self.get_server_id(), resource_id,
           r.learning_mode_expiry_time))
      # Schedules an action to log a message when this resource leaves
      # learning mode.
      scheduler.add_absolute(
          r.learning_mode_expiry_time + 1, _LeaveLearningMode(),
          (self.get_server_id(), r.resource_id))

    # Note: At this point this server has not capacity lease for this resource.
    # It is up to the caller to deal with this.
    assert r.IsInitialized()

    return r

  # Finds the state object for a resource/client combination. If
  # this client was not yet registered as a client for this particular
  # resource, adds it. Returns a tuple with the resource and the
  # client state for this resource. If the resource cannot be
  # found/created this method returns (None, None).
  def find_client_resource(self, client_id, resource_id):
    resource = self.find_resource(resource_id)

    # If we did not find the resource, return None.
    if not resource:
      return (None, None)

    # Linear scan for the client in this resource.
    for client in resource.client:
      if client.client_id == client_id:
        return (resource, client)

    # If we get here this client is not yet registered as a
    # client for this resource, in which case we simply
    # add it.
    client = resource.client.add()
    client.client_id = client_id

    return (resource, client)

  # Find the state object for a resource/server combination.
  # Same as find_client_resource, but for clients that are
  # servers.
  def find_server_resource(self, server_id, resource_id):
    resource = self.find_resource(resource_id)

    # Did we find the resource?
    if not resource:
      return (None, None)

    # Linear scan for the server in this resource.
    for server in resource.server:
      if server.server_id == server_id:
        return (resource, server)

    # This is a new server that has not contacted us about this
    # resource before.
    server = resource.server.add()
    server.server_id = server_id

    return (resource, server)

  # Generator for all resources in the configuration.
  def all_resources(self):
    for resource in self.wrapped_state.resource:
      yield resource

  # Finds the PriorityBandAggregateRequest for a particular priority.
  def _find_prio_band_agg(self, req, priority):
    for w in req.wants:
      if w.priority == priority:
        return w

    # It does not exist yet.
    w = req.wants.add()
    w.priority = priority
    w.num_clients = 0
    w.wants = 0

    assert w.IsInitialized()

    return w

  # Fills a request for a GetServerCapacity RPC.
  def fill_server_capacity_request(self):
    request = GetServerCapacityRequest()
    request.server_id = self.get_server_id()

    for resource in self.wrapped_state.resource:
      req = request.resource.add()
      req.resource_id = resource.resource_id

      if resource.HasField('has'):
        req.has.CopyFrom(resource.has)

      req.outstanding = sum_outstanding(resource)

      # Aggregate all the clients into the PriorityBandAggregateRequests for
      # this subrequest.
      for client in resource.client:
        w = self._find_prio_band_agg(req, client.priority)
        w.num_clients += 1
        w.wants += client.wants

      # And aggregate all the servers too.
      for server in resource.server:
        for prio_want in server.wants:
          w = self._find_prio_band_agg(req, prio_want.priority)
          w.num_clients += prio_want.num_clients
          w.wants += prio_want.wants

    assert request.IsInitialized()

    return request

  # Figures out if the lease on a resource has expired, and if so
  # clears the "has" field for that resource.
  # Note: We got to pass the resource id in here and not the resource
  # itself because there is a possibility that the resource reference
  # for the resource has changed due to cleanup or a removal plus
  # addition.
  def _maybe_lease_expired(self, resource_id):
    # If we are no longer the master this action does not need to'
    # be executed anymore.
    if not self.server.is_master():
      return

    resource = self.find_resource(resource_id)

    if lease_expired(resource):
      resource.ClearField('has')
      logger.info(
          '%s lease on capacity for resource %s expired' %
          (self.get_server_id(), resource.resource_id))
      Counter.get('server.lease_expired').inc()

  # Process the response of a GetServerCapacity RPC into the state.
  def process_capacity_response(self, response):
    for resp in response.resource:
      assert resp.gets.capacity >= 0

      resource = self.find_resource(resp.resource_id)
      n = sum_leases(resource)

      if resp.gets.capacity < n:
        logger.warning(
            '%s shortfall for %s: getting %lf, but has %lf outstanding leases' %
            (self.get_server_id(), resource.resource_id,
             resp.gets.capacity, n))
        Counter.get('server_capacity_shortfall').inc()
        Gauge.get('server.%s.shortfall' %
                  self.get_server_id()).set(resp.gets.capacity - n)

      resource.has.CopyFrom(resp.gets)

      # Schedules an action at the expirty time to clear out the lease.
      scheduler.add_absolute(
          resource.has.expiry_time,
          lambda: self._maybe_lease_expired(resource.resource_id))
