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


# All out massive scenario: 9 datacenters, three regions, one root
# server.
def scenario_five(reporter, num_clients=5):
  root_job = ServerJob('root', 0, 3)

  for i in xrange(1, 4):
    region_job = ServerJob('region:%i' % i, 1, 3, root_job)

    for j in xrange(1, 4):
      dc_job = ServerJob('dc:%d:%d' % (i, j), 2, 3, region_job)

      for k in xrange(1, num_clients + 1):
        client = Client('client:%d:%d' % (i, j), dc_job)
        client.add_resource('resource0', 0, 15, 0.1, 10)

  reporter.schedule('resource0')
  reporter.set_filename('scenario_five')


if __name__ == '__main__':
  run_scenario(lambda reporter: scenario_five(reporter))
