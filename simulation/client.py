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

import random
import sys

from client_state_pb2 import *
from protocol_pb2 import *
from scheduler import scheduler
from state_utils import *
from utils import clock, logger
from varz import Counter, Gauge


# The default refresh interval (in seconds) in cases the client
# calculates an improbable refresh interval (<=0).
_kDefaultRefreshInterval = 5

# The interval used to retry a failed discovery.
_kDefaultDiscoveryInterval = 5


# Scheduleable object that changes a resource's wants value by
# a random amount on an interval. Every time it runs it
# schedules itself again for the next change. When creating
# this object it immediately executes for the first time and
# then schedules the next instance.
class _ChangeWants(object):

  def __init__(self, client_id, resource, fraction, interval):
    self.client_id = client_id
    self.resource = resource
    self.fraction = fraction
    self.interval = interval

    self.execute(None)

  def execute(self, dummy):
    old = w = self.resource.wants
    w += self.fraction * (1 - 2 * random.random()) * w

    if w < 0:
      w = 0

    self.resource.wants = w
    logger.debug('%s changing wants from %lf to %lf' % (self.client_id, old, w))
    scheduler.add_relative(self.interval, self)
    Gauge.get('client.%s.wants' % self.client_id).set(w)


# Objects of this class mock a client using one or more resources and
# interacting with a rate limiting server to get capacity. The
class Client(object):
  # Class variables
  num_clients = dict()
  all_clients_list = list()

  # Creates a new client. Each client will get a unique name and takes
  # a reference to the server job that will get this client its
  # capacity limits for the resources it uses.
  def __init__(self, name, downstream_job):
    # Keeps a reference to the server job.
    self.downstream_job = downstream_job

    # We don't yet know who the master is.
    self.master = None

    # Creates a client state object to maintain resource state.
    self.state = ClientState()

    # Figures out the unique client id for this client and stores
    # it in the state.
    Client.num_clients.setdefault(name, 0)
    Client.num_clients[name] += 1
    self.state.client_id = '%s:%d' % (name, Client.num_clients[name])

    # Append this client to the list of all clients (used by the reporter).
    Client.all_clients_list.append(self)

    assert self.state.IsInitialized()

    # Kicks off the pseudo-thread that does discovery and getting capacity.
    scheduler.add_thread(self, 0)

  # Returns the client id of this client
  def get_client_id(self):
    return self.state.client_id

  # Generator that returns all clients in the system. Used by the reporter.
  @classmethod
  def all_clients(self):
    for client in self.all_clients_list:
      yield client

  # Returns a random client
  @classmethod
  def get_random_client(self):
    return self.all_clients_list[random.randint(0, len(self.all_clients_list) - 1)]

  # Sets the wanted capacity for this client and resource.
  def set_wants(self, resource_id, wants):
    resource = self._find_resource(resource_id)
    resource.wants = wants

  # Returns the current wanted capacity for this client and resource.
  def get_wants(self, resource_id):
    return self._find_resource(resource_id).wants

  # Finds the resource information in the client state. Returns None if
  # the resource is not registered.
  def _find_resource(self, resource_id):
    for r in self.state.resource:
      if r.resource_id == resource_id:
        return r

    return None

  # Adds a resource to this client's state. From then one the resource
  # will have its "wants" changed randomly (on the interval) and the
  # client will ask for capacity limits for this resource.
  def add_resource(self, resource_id, priority, wants, fraction=0, interval=1):
    # Checks that the resource is not yet present; that would be a fatal
    # error.
    r = self._find_resource(resource_id)

    assert not r

    # Adds a new resource object to the state and stores the resource
    # information in it.
    r = self.state.resource.add()
    r.resource_id = resource_id
    r.priority = priority
    r.wants = wants

    # No need to kick off the process of randomly changing the "wants"
    # of this resource if the fraction is zero.
    if fraction > 0:
      assert interval > 0
      _ChangeWants(self.state.client_id, r, fraction, interval)

    # Immediately try and get some capacity for this resource (and,
    # as a consequence, for all other resources as well).
    scheduler.update_thread(self, 0)

  # Returns the reporting data tuple for this client. Called by
  # the reporter.
  def get_reporting_data(self, resource_id):
    from reporter import ReportingData

    resource = self._find_resource(resource_id)
    data = ReportingData()
    data.wants = resource.wants
    data.has = resource.has.capacity

    return data

  # Sends a Discovery RPC to a random task in the server job. Returns
  # a reference to the server we discovered, or Non if we have no
  # clue who the master is.
  def _discover(self):
    assert self.state.IsInitialized()

    request = DiscoveryRequest()
    request.client_id = self.state.client_id

    # Adds all the resources we know about so that we can get
    # the safe capacities for them.
    for r in self.state.resource:
      request.resource_id.append(r.resource_id)

    # Sends the request to a random task in the server job.
    response = self.downstream_job.get_random_task().Discovery_RPC(request)

    # If the response has a master_bns field we store the reference
    # to the master. If not there is no master, which would suck.
    if response.HasField('master_bns'):
      self.master = self.downstream_job.get_task_by_name(response.master_bns)
    else:
      self.master = None
      logger.warning('%s doesn\'t know who the master is.' %
                     self.state.client_id)
      Counter.get('client.discovery_failure').inc()

    # Goes through the response and stores all the safe capacities in the
    # client state.
    for safe in response.safe_capacity:
      self._find_resource(safe.resource_id).safe_capacity = safe.safe_capacity

    # Returns the server we just discovered to be the master.
    return self.master

  # Figures out when to get capcity. This assumes that we just did a
  # GetCapacity RPC so we only need to find the lowest refresh_interval
  # of all the resources in the resource state.
  def _renew_capacity_interval(self):
    # Figures out the smallest refresh_interval in the client state.
    delay = sys.maxint

    for r in self.state.resource:
      if r.HasField('has'):
        delay = min(delay, r.has.refresh_interval)

    # If that delay is highly improbable we have some error and we use
    # a default delay.
    if delay <= 0 or delay == sys.maxint:
      logger.error(
          '%s improbable delay %d, set to %d instead' %
          (self.state.client_id, delay, _kDefaultRefreshInterval))
      delay = _kDefaultRefreshInterval
      Counter.get('client.improbable.delay').inc()

    return delay

  # Figures out if the lease on a resource has expired, and if so
  # clears the "has" field for that resource.
  # Note: We got to pass the resource id in here and not the resource
  # itself because there is a possibility that the resource reference
  # for the resource has changed due to cleanup or a removal plus
  # addition.
  def _maybe_lease_expired(self, resource_id):
    resource = self._find_resource(resource_id)

    if lease_expired(resource):
      resource.ClearField('has')
      logger.info(
          '%s lease on capacity for resource %s expired' %
          (self.get_client_id(), resource.resource_id))
      Counter.get('client.lease_expired').inc()

  # Gets capacity for all the resources this client uses by calling
  # the GetCapacity RPC in the master. Returns a boolean to indicate
  # whether the RPC failed or not.
  def _get_capacity(self):
    assert self.state.IsInitialized()
    assert self.master

    # If there are no resources in the state we're done.
    if len(self.state.resource) == 0:
      return

    # Creates the RPC request object.
    request = GetCapacityRequest()
    request.client_id = self.state.client_id

    # Goes through all the resources in the state and adds a subrequest
    # for that resource to the request.
    for res in self.state.resource:
      req = request.resource.add()
      req.resource_id = res.resource_id
      req.priority = res.priority
      req.wants = res.wants

      if res.HasField('has'):
        req.has.CopyFrom(res.has)

      # Calls the GetCapacity RPC in the master.
      response = self.master.GetCapacity_RPC(request)

      # If that failed we did not get new capacities. Blimey! Most probably
      # the server we talked to is no longer the master, so schedule a discovery
      # asap. When that discovery succeeds it will call us again.
      if not response:
        logger.error('%s GetCapacity request failed!' % self.get_client_id())
        Counter.get('client.GetCapacity_RPC.failure').inc()
        return False
      else:
        # Goes through the response and copies the capacity information back into
        # the client state.
        for r in response.response:
          assert r.gets.capacity >= 0

          resource = self._find_resource(r.resource_id)
          resource.has.CopyFrom(r.gets)

          # Schedules an action at the expiry time to clear out the lease.
          scheduler.add_absolute(
              resource.has.expiry_time,
              lambda: self._maybe_lease_expired(r.resource_id))

          if r.HasField('safe_capacity'):
            resource.safe_capacity = r.safe_capacity
          else:
            resource.ClearField('safe_capacity')

    return True

  # This is the main function of the pseudo-thread. It needs to
  # figure out what needs to be done, then do it, and return
  # the timestamp when the next action needs to be scheduled.
  def thread_continue(self):
    # If we don't know who the master is let's figure this out.
    if not self.master:
      # If discovery failed, try another discovery in the
      # near future
      if not self._discover():
        return _kDefaultDiscoveryInterval

    # We know who the master is, let's get some capacity. If this
    # fails we need to reschedule a discovery.
    if not self._get_capacity():
      self.master = None

      return 0

    # Returns the interval in which we need to refresh our capacity
    # leases.
    return self._renew_capacity_interval()
