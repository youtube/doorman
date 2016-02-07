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

import re
import sys

from algorithm import AlgorithmImpl
from config_wrapper import global_config
from protocol_pb2 import *
from scheduler import scheduler
from server_state_wrapper import ServerStateWrapper
from utils import clock, logger
from varz import Counter, Gauge


# The default lease time for unknown resources
_kDefaultLeaseTimeForUnknownResources = 300

# The minimum time between two requests from a client.
_kMinimumInterval = 2

# The default delay to use for a capacity refresh if we calculate an
# improbable delay.
_kDefaultRefreshInterval = 5

# The default delay for retrying a failed discovery.
_kDefaultDiscoveryInterval = 5

# The end of time (one week :-)
_kTheEndOfTime = 86400


# The core class which operates a rate limiting server. This class
# mimicks a single master task, which is supposed to belong to a
# ServerJob. The job will call some methods in the server to inform
# it about becoming the master or losing mastership.
class Server(object):
  # Used to generate server identifiers.
  num_servers = dict()

  # Constructor.
  def __init__(self, job, job_name, server_level, downstream_job=None):
    if server_level == 0:
      assert downstream_job is None
    else:
      assert downstream_job is not None

    self.job = job
    self.downstream_job = downstream_job
    self.master = None
    self.server_level = server_level

    Server.num_servers.setdefault(job_name, 0)
    Server.num_servers[job_name] += 1
    self.server_id = '%s:%d' % (job_name, Server.num_servers[job_name])

    self.state = ServerStateWrapper(self)

    # Kick off the pseudo-thread to do discovery and get resource
    # capacity.
    scheduler.add_thread(self, 0)

  def get_server_id(self):
    return self.server_id

  def get_server_level(self):
    return self.server_level

  def is_master(self):
    return self.state.get_election_victory_time() != None

  # Tells this server that it is no longer the master. This will reset
  # the internal state.
  def lose_mastership(self):
    assert self.is_master()

    logger.info('%s losing mastership' % self.server_id)
    self.state.reset()

  # Tells this server that it has become the master (as result of a
  # master election having been triggered).
  def become_master(self):
    assert not self.is_master()

    logger.info('%s becoming master' % self.server_id)
    self.state.assert_clean()
    self.state.set_election_victory_time()

    # Wake up the thread that does discovery and getting capacity.
    scheduler.update_thread(self, 0)

  # Returns the reporting data for this server. Just delegates to the
  # wrapped state object.
  def get_reporting_data(self, resource_id):
    assert self.is_master()

    return self.state.get_reporting_data(resource_id)

  # Sends a Discovery RPC to a random task in the server job.
  # This differs from the _discover method in the client code that
  # here we are not interested in the safe capacities. Returns a
  # reference to the server tasks that is the master, or None
  # if we did not find one.
  def _discover(self):
    assert self.server_level > 0

    request = DiscoveryRequest()
    request.client_id = self.server_id

    # Sends the request to a random task in the server job.
    response = self.downstream_job.get_random_task().Discovery_RPC(request)

    # If the response has a master_bns field we store the reference
    # to the master. If not there is no master, which would suck.
    if response.HasField('master_bns'):
      self.master = self.downstream_job.get_task_by_name(response.master_bns)
    else:
      self.master = None
      logger.warning('%s doesn\'t know who the master is.' % self.server_id)
      Counter.get('server.discovery_failure').inc()

    return self.master

  # Implements the Discovery RPC.
  def Discovery_RPC(self, request):
    assert request.IsInitialized()

    timer = Gauge.get('server.DiscoveryRPC.latency')
    timer.start_timer()
    logger.info(
        '%s handling Discovery RPC from %s' %
        (self.server_id, request.client_id))
    response = DiscoveryResponse()

    # Sets the master_bns field in the response if there is a current
    # master.
    master = self.job.get_master()

    if master:
      response.master_bns = master.get_server_id()
    else:
      # We don't know who the master is.
      Counter.get('server.incomplete_discovery_response').inc()

    # Goes through the resource ids in the request and sets the
    # safe capacity for every resource that has a safe capacity
    # configured.
    for r in request.resource_id:
      t = global_config.find_resource_template(r)

      if t and t.HasField('safe_capacity'):
        safe = response.safe_capacity.add()
        safe.resource_id = r
        safe.safe_capacity = t.safe_capacity

    assert response.IsInitialized()

    timer.stop_timer()

    return response

  # Figured out when to execute the next _get_capacity call. The interval is determined by the
  # refresh_interval settings of the resources in the state.
  def _renew_capacity_interval(self):
    # Figures out the smallest refresh_interval in the server state.
    delay = sys.maxint

    for resource in self.state.all_resources():
      if resource.HasField('has'):
        delay = min(delay, resource.has.refresh_interval)

    # If that delay is highly improbable we have some error and we use
    # a default delay. This might for instance happen if all resources
    # have lost their (or never gotten any) leases.
    if delay <= 0 or delay == sys.maxint:
      logger.error(
          '%s improbable delay %d, set to %d instead' %
          (self.server_id, delay, _kDefaultRefreshInterval))
      delay = _kDefaultRefreshInterval
      Counter.get('server.improbable.delay').inc()

    return delay

  # Get some capacity from the master downstream server.
  def _get_capacity_downstream(self):
    response = self.master.GetServerCapacity_RPC(
        self.state.fill_server_capacity_request())

    # Did the RPC fail?
    if not response:
      return False

    # Work the response into the state.
    self.state.process_capacity_response(response)

    return True

  # Get some capacity for this server to hand out. Returns a boolean to
  # indicate whether this succeeded or failed.
  def _get_capacity(self):
    assert self.is_master()

    now = clock.get_time()

    # Assume the worst... :-)
    success = False

    # If we are server level 0, we need to get the capacity from the
    # configuration.
    if self.server_level == 0:
      for resource in self.state.all_resources():
        algo = AlgorithmImpl.create(resource.template, self.server_level)
        resource.ClearField('has')
        resource.has.CopyFrom(
            algo.create_lease(resource, resource.template.capacity))

        # Note, we set a refresh interval here even though the capacity we get from the
        # configuration lasts forever. However by setting a refresh interval and relatively
        # short leases we ensure that configuration changes (e.g. from CDD) are
        # picked up.
        resource.has.refresh_interval *= 2

      success = True
    else:
      # If this is not the root server it gets its capacity from
      # a downstream server.
      success = self._get_capacity_downstream()

    logger.info('%s resource state after getting capacity:' % self.server_id)

    for resource in self.state.all_resources():
      logger.info(
          'resource: %s got: %lf lease: %d refresh: %d' %
          (resource.resource_id, resource.has.capacity,
           resource.has.expiry_time - now, resource.has.refresh_interval))

    return success

  # Implements the GetServerCapacity RPC.
  def GetServerCapacity_RPC(self, request):
    assert request.IsInitialized()
    assert self.state.is_initialized()

    # Only the master can handle this RPC.
    if not self.is_master():
      self.state.assert_clean()
      logger.info(
          '%s getting a GetServerCapacity request when not master' %
          self.server_id)

      Counter.get('server.GetServerCapacity_RPC.not_master').inc()

      return None

    gauge = Gauge.get('server.GetServerCapacity_RPC.latency')
    gauge.start_timer()
    logger.debug(request)
    now = clock.get_time()

    # Cleans the state. This removes resources and clients with expired
    # leases and such.
    self.state.cleanup()

    # A set of resources that we need to skip in step 2 (the actual
    # handing out of capacity.
    resources_to_skip = set()

    # First step: Go through the request and update the state with the
    # information from the request.
    for req in request.resource:
      (resource, sr) = (
          self.state.find_server_resource(
              request.server_id,
              req.resource_id))

      # If this resource does not exist we don't need to do anything right now.
      if resource:
        assert sr

        # Checks whether the last request from this server was at least
        # _kMinimumInterval seconds ago.
        if sr.HasField('last_request_time') and now - sr.last_request_time < _kMinimumInterval:
          logger.warning(
              '%s GetServerCapacity request for resource %s within the %d '
              'second threshold' %
              (self.server_id, req.resource_id, _kMinimumInterval))
          resources_to_skip.add(req.resource_id)
        else:
          # Updates the state with the information in the request.
          sr.last_request_time = now
          sr.outstanding = req.outstanding
          del sr.wants[:]

          for w in req.wants:
            sr.wants.add().CopyFrom(w)

          if req.HasField('has'):
            sr.has.CopyFrom(req.has)
          else:
            sr.ClearField('has')

    # Creates a new response object in which we will insert the response for
    # the resources contained in the request.
    response = GetServerCapacityResponse()

    # Step 2: Loop through all the individual resource requests in the request
    # and hand out capacity.
    for req in request.resource:
      # If this is a resource we need to skip, let's skip it.
      if req.resource_id in resources_to_skip:
        continue

      # Finds the resource and the client state for this resource.
      (resource, sr) = (
          self.state.find_server_resource(
              request.server_id,
              req.resource_id))

      # Adds a response proto to the overall response.
      resp = response.resource.add()
      resp.resource_id = req.resource_id

      # If this is an unknown resource just give the client whatever it
      # is asking for.
      if not resource:
        assert not sr

        logger.warning(
            '%s GetServerCapacity request for unmanaged resource %s' %
            (self.server_id, req.resource_id))
        resp.gets.expiry_time = now + _kDefaultLeaseTimeForUnknownResources
        resp.gets.capacity = req.wants
      else:
        # Finds the algorithm implementation object for this resource.
        algo = AlgorithmImpl.create(resource.template, self.server_level)

        # If the resource is in learning mode we just return whatever the client
        # has now and create a default lease.
        if resource.learning_mode_expiry_time >= now:
          if sr.HasField('has'):
            has_now = sr.has.capacity
          else:
            has_now = 0

          sr.has.CopyFrom(algo.create_lease(resource, has_now))
        else:
          # Otherwise we just run the algorithm. This will update the
          # client state object.
          algo.run_server(resource, sr)

        # Copies the output from the algorithm run into the response.
        resp.gets.CopyFrom(sr.has)

      assert resp.IsInitialized()
      logger.info(
          '%s for %s resource: %s wants: %lf gets: %lf lease: %d refresh: %d' %
          (self.server_id, request.server_id, req.resource_id,
           sum([w.wants for w in req.wants]), resp.gets.capacity,
           resp.gets.expiry_time - now, resp.gets.refresh_interval))

    assert response.IsInitialized()
 
    gauge.stop_timer()

    return response

  # Implements the GetCapacity RPC.
  def GetCapacity_RPC(self, request):
    assert request.IsInitialized()
    assert self.state.is_initialized()

    # If this server is not the master it cannot handle this request.
    # The client should do a new Discovery.
    if not self.is_master():
      self.state.assert_clean()
      logger.info('%s getting a GetCapacity request when not master' %
                  self.server_id)
      Counter.get('server.GetCapacity_RPC.not_master').inc()

      return None

    timer = Gauge.get('server.GetCapacity_RPC.latency')
    timer.start_timer()
    logger.debug(request)
    now = clock.get_time()

    # Cleanup the state. This removes resources and clients with expired
    # leases and such.
    self.state.cleanup()

    # A set of resources that we need to skip in step 2 (the actual
    # handing out of capacity.
    resources_to_skip = set()

    # First step: Go through the request and update the state with the
    # information from the request.
    for req in request.resource:
       # Finds the resource and the client state for this resource.
      (resource, cr) = self.state.find_client_resource(
          request.client_id,
          req.resource_id)

      # If this resource does not exist we don't need to do anything
      # right now.
      if resource:
        assert cr

        # Checks whether the last request from this client was at least
        # _kMinimumInterval seconds ago.
        if cr.HasField('last_request_time') and now - cr.last_request_time < _kMinimumInterval:
          logger.warning(
              '%s GetCapacity request for resource %s within the %d second '
              'threshold' %
              (self.server_id, req.resource_id, _kMinimumInterval))
          resources_to_skip.add(req.resource_id)
        else:
          # Updates the state with the information in the request.
          cr.last_request_time = now
          cr.priority = req.priority
          cr.wants = req.wants

          if req.HasField('has'):
            cr.has.CopyFrom(req.has)
          else:
            cr.ClearField('has')

    # Creates a new response object in which we will insert the responses for
    # the resources contained in the request.
    response = GetCapacityResponse()

    # Step 2: Loop through all the individual resource requests in the request
    # and hand out capacity.
    for req in request.resource:
      # If this is a resource we need to skip, let's skip it.
      if req.resource_id in resources_to_skip:
        continue

      # Finds the resource and the client state for this resource.
      (resource, cr) = (
          self.state.find_client_resource(
              request.client_id,
              req.resource_id))

      # Adds a response proto to the overall response.
      resp = response.response.add()
      resp.resource_id = req.resource_id

      # If this is an unknown resource just give the client whatever it
      # is asking for.
      if not resource:
        assert not cr

        logger.warning(
            '%s GetCapacity request for unmanaged resource %s' %
            (self.server_id, req.resource_id))
        resp.gets.expiry_time = now + _kDefaultLeaseTimeForUnknownResources
        resp.gets.capacity = req.wants
      else:
        # Sets the safe capacity in the response if there is one
        # configured for this resource.
        if resource.template.HasField('safe_capacity'):
          resp.safe_capacity = resource.template.safe_capacity

        # Finds the algorithm implementation object for this resource.
        algo = AlgorithmImpl.create(resource.template, self.server_level)

        # If the resource is in learning mode we just return whatever the client
        # has now and create a default lease.
        if resource.learning_mode_expiry_time >= now:
          if cr.HasField('has'):
            has_now = cr.has.capacity
          else:
            has_now = 0

          cr.has.CopyFrom(algo.create_lease(resource, has_now))
          Counter.get('server.learning_mode_response').inc()
        else:
          # Otherwise we just run the algorithm. This will update the
          # client state object.
          algo.run_client(resource, cr)
          Counter.get('server.algorithm_runs').inc()

        # Copies the output from the algorithm run into the response.
        resp.gets.CopyFrom(cr.has)

      assert resp.IsInitialized()
      logger.info(
          '%s for %s resource: %s wants: %lf gets: %lf lease: %d refresh: %d' %
          (self.server_id, request.client_id, req.resource_id, req.wants,
           resp.gets.capacity, resp.gets.expiry_time - now,
           resp.gets.refresh_interval))

    assert response.IsInitialized()

    timer.stop_timer()

    return response

  # This is the main function of the pseudo-thread. It needs to
  # figure out what needs to be done, then do it, and return
  # the timestamp when the next action needs to be scheduled.
  def thread_continue(self):
    # If we are not the master server in the job, we don't need to
    # do anything. Our next scheduled action is at the end of time.
    # Note: When we become the master we will update this interval.
    if not self.is_master():
      Counter.get('server.halt_thread').inc()
      return _kTheEndOfTime

    # If this is not the root server we might need to do a discovery.
    if self.server_level > 0:
      # If we don't know who the master is let's figure this out.
      if not self.master:
        # If discovery failed, try another discovery in the
        # near future
        if not self._discover():
          return _kDefaultDiscoveryInterval

    # Either we know who the master is or we don't need to know because
    # we are the root server. Let's get some capacity. If this
    # fails we need to reschedule a discovery.
    if not self._get_capacity():
      Counter.get('server.reschedule_discovery').inc()
      self.master = None

      return 0

    # Returns the interval in which we need to refresh our capacity
    # leases.
    return self._renew_capacity_interval()
