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
import time


# A simple counter class used for some basic instrumentation,
class Counter(object):
  # This class member contains references to all counters in
  # existence.
  counters = dict()

  # Factory method for getting a unique Counter instance. This
  # method will create the counter if necessary.
  @classmethod
  def get(self, name):
    if not name in self.counters:
      self.counters[name] = Counter(name)

    return self.counters[name]

  # Generator method to access all counters. Used by the Reporter.
  @classmethod
  def all_counters(self):
    for counter in self.counters.values():
      yield counter

  # Creates a new counter.
  def __init__(self, name, value=0):
    self.value = value
    self.name = name

  # Returns the counter's name.
  def get_name(self):
    return self.name

  # Increases the counter.
  def inc(self, n=1):
    self.value += n

  # Returns the current value of the counter.
  def get_value(self):
    return self.value

# A simple gauge class used for some basic instrumentation. The
# Gauge maintains minimum, maximum and average values as well.


class Gauge(object):
  # This class member contains references to all gauges in
  # existence.
  gauges = dict()

  @classmethod
  def get(self, name):
    if not name in self.gauges:
      self.gauges[name] = Gauge(name)

    return self.gauges[name]

  # Generator method to access all gauges. Used by the Reporter.
  @classmethod
  def all_gauges(self):
    for gauge in self.gauges.values():
      yield gauge

  # Creates a new gauge.
  def __init__(self, name):
    self.value = 0
    self.n = 0
    self.max_value = -sys.maxint
    self.min_value = sys.maxint
    self.average = 0
    self.name = name
    self.timer_start = 0

  # Returns the gauge's name.
  def get_name(self):
    return self.name

  # Sets the new value of the gauge. This also updates the min/max/avg.
  def set(self, value):
    self.value = value
    self.max_value = max(value, self.max_value)
    self.min_value = min(value, self.min_value)
    self.n += 1
    self.average = ((self.n - 1) * self.average) / self.n + self.value / self.n

  # Starts a timer. This can be used to time certain actions. The gauge gets
  # updated when the stop_timer method is called.
  def start_timer(self):
    assert self.timer_start == 0
    self.timer_start = time.time()

  # Resets the timer. The gauge is not updated.
  def reset_timer(self):
    self.timer_start = 0

  # Stops the timer and updates the gauge. This method can only be called after
  # start_timer has been called.
  def stop_timer(self):
    assert self.timer_start != 0
    self.set(time.time() - self.timer_start)
    self.reset_timer()

  # Returns the current value of the gauge.
  def get_value(self):
    return self.value

  # Returns the number of updates which happened to the gauge.
  def get_count(self):
    return self.n

  # Returns the minimum value we ever saw for the gauge. If the
  # gauge was never assigned the value is sys.maxint.
  def get_min_value(self):
    return self.min_value

  # Returns the maximum value we ever saw for the gauge. If the
  # gauge was never assigned the value is -sys.maxint.
  def get_max_value(self):
    return self.max_value

  # Returns the average value of the gauge.
  def get_average(self):
    return self.average
