#!/usr/bin/python2.7

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

from client import Client
from scenario_five import scenario_five
from scheduler import scheduler
from server_job import ServerJob
from simulation import run_scenario
from utils import logger
from varz import Counter


def spike_client():
  client = Client.get_random_client()
  n = client.get_wants('resource0') + 100
  logger.info('Random mishap: Setting %s wants to %d' % (client.get_client_id(), n))
  client.set_wants('resource0', n)


def trigger_master_election():
  job = ServerJob.get_random_server_job()
  logger.info('Random mishap: Triggering master election in job %s' % job.get_job_name())
  job.trigger_master_election()


def lose_master():
  job = ServerJob.get_random_server_job()
  t = random.randint(0, 60)
  logger.info('Random mishap: Losing master job %s for %d seconds' % (job.get_job_name(), t))
  job.lose_master()
  scheduler.add_relative(t, lambda: job.trigger_master_election())

_mishap_map = dict([
  (5, lambda: spike_client()),
  (10, lambda: trigger_master_election()),
  (15, lambda: lose_master()),
])

# Invoke some random mishap.
def random_mishap():
  scheduler.add_relative(60, lambda: random_mishap())

  total = max(_mishap_map.keys())
  m = random.randint(0, total - 1)
  n = 0

  for (key, value) in _mishap_map.iteritems():
    if n >= m:
      Counter.get('mishap.%d' % key).inc()
      value()
      return

    n += key


  assert False

# Uses the setup of scenario five, but runs for a (simulated) hour
# and invokes random mishap every 60 seconds.
def scenario_seven(reporter):
  scenario_five(reporter)
  reporter.set_filename('scenario_seven')
  scheduler.add_absolute(60, lambda: random_mishap())

if __name__ == '__main__':
  run_scenario(lambda reporter: scenario_seven(reporter), run_for=3600)
