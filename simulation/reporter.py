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

from client import Client
from server_job import ServerJob
from scheduler import scheduler
from utils import clock, logger
from varz import Counter, Gauge


# A dummy class to hold arbitrary reporting data
class ReportingData(object):
  pass


# This class provides functionality to capture reporting data
# throughout the operation of the system and then write out a CSV file
# with all the reporting data. The reporter gathers reporting data
# every 5 seconds.
class Reporter(object):

  # Creates a new reporter.
  def __init__(self):
    self.data = dict()
    self.summaries = dict()
    self.all_clients = set()
    self.all_server_jobs = set()
    self.all_summaries = set()
    self.filename = None

    scheduler.add_finalizer(self)

  # Finalizer generates the final report.
  def finalize(self):
    self.final_report()

  # Schedules a gathering run for a given resource id.
  def schedule(self, resource_id):
    scheduler.add_relative(5, self, resource_id)

  # This is the callback for the scheduler. This method calls the
  # report data gatherer method and schedules itself again for the
  # next run.
  def execute(self, resource_id):
    self.gather_reporting_data(resource_id)
    self.schedule(resource_id)

  # Sets the CSV output filename. If none is ever set the report
  # goes to stdout.
  def set_filename(self, filename):
    self.filename = '%s.csv' % filename

  # Gathers reporting data on a given resource id and stores it into
  # the reporter's internal state.
  def gather_reporting_data(self, resource_id):
    logger.info('Gathering reporting data')
    now = clock.get_time()

    # Adds a record to the data set for this timestamp.
    self.data[now] = dict()
    self.summaries[now] = dict()

    # Adds a summary record for the clients
    p = ReportingData()
    p.total_wants = 0
    p.total_has = 0
    self.summaries[now]['clients'] = p
    self.all_summaries.add('clients')

    # Step 1: Goes through all the clients in the system, gets their
    # reporting data and adds it to the data set.
    for client in Client.all_clients():
      client_id = client.get_client_id()
      self.all_clients.add(client_id)
      data = client.get_reporting_data(resource_id)

      if data:
        self.data[now][client_id] = data
        logger.debug('%s: %s' % (client_id, str(data)))
        p.total_wants += data.wants
        p.total_has += data.has
      else:
        logger.warning('No reporting data received from %s' % client_id)

    # Step 2: Find the master server of every job, get its reporting data
    # and add it to the data set.
    for job in ServerJob.all_server_jobs():
      current_master = job.get_master()

      # If this job does not have a master then we got nothing to do.
      if not current_master:
        continue

      job_name = job.get_job_name()
      self.all_server_jobs.add(job_name)
      data = current_master.get_reporting_data(resource_id)

      if data:
        self.data[now][job_name] = data
        logger.debug('%s: %s' % (job_name, str(data)))
        key = 'level %d' % current_master.get_server_level()
        self.all_summaries.add(key)

        if not key in self.summaries[now]:
          p = ReportingData()
          p.total_wants = 0
          p.total_has = 0
          p.total_leases = 0
          p.total_outstanding = 0
          self.summaries[now][key] = p
        else:
          p = self.summaries[now][key]

        p.total_wants += data.wants
        p.total_has += data.has
        p.total_leases += data.leases
        p.total_outstanding += data.outstanding
      else:
        logger.warning(
            'No reporting data received from %s' %
            current_master.get_server_id())

  # Writes the final report to a CSV file, either on disk (if set_filename)
  # has been called, or to stdout.
  def final_report(self):
    if self.filename:
      fout = open(self.filename, 'w')
    else:
      fout = sys.stdout

    # Prints the first header line.
    print >>fout, ',',

    for client in sorted(self.all_clients):
      print >>fout, '"%s"' % client, ',,',

    print >>fout, ',',

    for server in sorted(self.all_server_jobs):
      print >>fout, '"%s"' % server, ',,,,',

    print >>fout, ',',

    for s in sorted(self.all_summaries):
      print >>fout, '"%s"' % s,

      if s == 'clients':
        print >>fout, ',,',
      else:
        print >>fout, ',,,,',

    print >>fout

    # Prints the second header line.
    print >>fout, '"Time",',

    for client in sorted(self.all_clients):
      print >>fout, '"wants", "has",',

    print >>fout, ',',

    for server in sorted(self.all_server_jobs):
      print >>fout, '"wants", "has", "leases", "outstanding",',

    print >>fout, ',',

    for s in self.all_summaries:
      if s == 'clients':
        print >>fout, '"total_wants", "total_has",',
      else:
        print >>fout, ('"total_wants", "total_has", "total_leases", '
                       '"total_outstanding",'),

    print >>fout

    # Goes through the data set in timestamp order.
    for time in sorted(self.data.keys()):
      print >>fout, time, ',',
      data = self.data[time]

      # Prints the reporting data for every client and server that we ever saw.
      # If we have no data for a timestamp we print nothing.
      for client in sorted(self.all_clients):
        if client in data:
          d = data[client]
          print >>fout, d.wants, ',', d.has, ',',
        else:
          print >>fout, ',,',

      print >>fout, ',',

      # Do the same for the servers.
      for server in sorted(self.all_server_jobs):
        if server in data:
          d = data[server]
          print >>fout, d.wants, ',', d.has, ',', d.leases, ',', d.outstanding, ',',
        else:
          print >>fout, ',,,,',

      # Now for the summaries
      print >>fout, ',',

      data = self.summaries[time]

      for s in sorted(self.all_summaries):
        if not s in data:
          if s == 'clients':
            print >>fout, ',,',
          else:
            print >>fout, ',,,,',

          continue

        d = data[s]

        if s == 'clients':
          print >>fout, d.total_wants, ',', d.total_has, ',',
        else:
          print >>fout, d.total_wants, ',', d.total_has, ',', d.total_leases, ',', d.total_outstanding, ',',

      print >>fout

    # Now we go an print the counters.
    print >>fout
    print >>fout, '"Name", "Value"'
    names = list()

    for counter in Counter.all_counters():
      names.append(counter.get_name())

    for name in sorted(names):
      counter = Counter.get(name)
      print >>fout, counter.get_name(), ',', counter.get_value()

    # And all the gauges.
    print >>fout
    print >>fout, '"Name", "N", "Min", "Average", "Max"'
    names = list()

    for gauge in Gauge.all_gauges():
      names.append(gauge.get_name())

    for name in sorted(names):
      gauge = Gauge.get(name)
      print >>fout, gauge.get_name(), ',', gauge.get_count(), ',', gauge.get_min_value(
          ), ',', gauge.get_average(), ',', gauge.get_max_value()

    # Closes the output file.
    if self.filename:
      fout.close()
      logger.info('Report written to %s' % self.filename)
