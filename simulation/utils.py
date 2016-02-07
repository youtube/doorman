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

import logging
import time

kLevel = logging.INFO


# A local clock which drives the simulation. Always starts at zero and
# returns times relative to the starting time.
class _Clock(object):

  def __init__(self):
    self.time = 0

  def get_time(self):
    return self.time

  def advance(self, n):
    assert n >= 0
    self.time += n

  def set_time(self, t):
    # The clock can only move forward.
    assert t >= self.time
    self.time = t


# A custom formatter to add the timestamp from the simulated clock.
class _Formatter(logging.Formatter):

  def format(self, record):
    record.simulated_clock = "t=%04d" % clock.get_time()
    return super(_Formatter, self).format(record)

# Creates a logger object.


def _create_logger():
  logger = logging.getLogger("simulation")
  logger.setLevel(kLevel)
  ch = logging.StreamHandler()
  ch.setLevel(kLevel)
  formatter = _Formatter("%(simulated_clock)s - %(levelname)s - %(message)s")
  ch.setFormatter(formatter)
  logger.addHandler(ch)

  return logger

# Exports singleton objects for the clock and the logger.
clock = _Clock()
logger = _create_logger()
