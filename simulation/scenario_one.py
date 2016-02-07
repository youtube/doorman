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

from client import Client
from server_job import ServerJob
from simulation import run_scenario


# Scenario 1: A simple scenario to show convergence.
# resource0 has capacity of 500.  Root server job with three tasks, five
# clients with randomly varying resource needs.
def scenario_one(reporter):
  job = ServerJob('root', 0, 3)

  for i in xrange(0, 5):
    c = Client('client', job)
    c.add_resource('resource0', 0, 110, 0.1, 10)

  reporter.schedule('resource0')
  reporter.set_filename('scenario_one')

  return job


if __name__ == '__main__':
  run_scenario(lambda reporter: scenario_one(reporter))
