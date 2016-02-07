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
from scenario_five import scenario_five
from scheduler import scheduler
from simulation import run_scenario


# Choose two random clients and spike their wanted capacity to 1000.
def spike():
  client1 = Client.get_random_client()
  client2 = client1

  while client1 == client2:
    client2 = Client.get_random_client()

  client1.set_wants('resource0', 1000)
  client2.set_wants('resource0', 1000)


# Like scenario five, but at t=150 two random clients spike their
# wanted capacity to 1000.
def scenario_six(reporter):
  scenario_five(reporter)
  reporter.set_filename('scenario_six')

  scheduler.add_absolute(150, lambda: spike())

if __name__ == '__main__':
  run_scenario(lambda reporter: scenario_six(reporter))
