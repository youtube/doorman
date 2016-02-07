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

from scenario_one import scenario_one
from scheduler import scheduler
from simulation import run_scenario


# Scenario 2: Same as scenario 1, but with server failure and
# master election at T=120.
def scenario_two(reporter):
  job = scenario_one(reporter)
  scheduler.add_relative(120, lambda: job.lose_master())
  scheduler.add_relative(140, lambda: job.trigger_master_election())
  reporter.set_filename('scenario_two')


if __name__ == '__main__':
  run_scenario(lambda reporter: scenario_two(reporter))
