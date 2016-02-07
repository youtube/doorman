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

import time
import types

from utils import clock, logger
from varz import Gauge


# A scheduler that allows for scheduling actions in the future (or the
# past, though time travel is not implemented yet). The main loop of
# the scheduler then executes the scheduled actions at the appropriate
# time. The Scheduler also supports pseudo-threads.
class Scheduler(object):

  def __init__(self):
    self.schedule = dict()
    self.thread_map = dict()
    self.finalizers = list()

  # Adds a new pseudo-thread to the scheduler. Each pseudo-thread is
  # an object with a thread_continue(self) method. The scheduler keeps
  # track of when each pseudo-thread should continue (in absolute time
  # on the simulated clock). The thread_contnue method should return
  # the next interval on which the thread should continue.
  def add_thread(self, thread, interval):
    self.update_thread(thread, interval)

  # Updates the time when the pseudo-thread should continue.
  def update_thread(self, thread, interval):
    self.thread_map[thread] = clock.get_time() + interval

  # Adds a finalizer. This is a lambda or object (finalize() method)
  # that gets called when the scheduler exits.
  def add_finalizer(self, target):
    self.finalizers.append(target)

  # Adds a new action at a specified time. The target can be an object
  # with an execute(arg) method or a lambda without any methods.
  # Note: The entire scheduler system is single threaded. That means
  # that it is not possible to add something here while the main loop
  # is sleeping! The simple fact that we can execute this means that
  # the main loop is not sleeping, so we can safely add whatever we
  # want without worrying that the scheduler might miss it.
  def add_absolute(self, time, target, arg=None):
    if time < clock.get_time():
      logger.warning('Scheduling action in the past!')

    if type(target) == types.LambdaType and not arg is None:
      logger.warning('Non-None argument ignored for lambda callback')

    # Adds the schedulable item (a target and argument tuple) to the
    # schedule.
    item = (target, arg)
    self.schedule.setdefault(time, list())
    self.schedule[time].append((target, arg))

    return time

  # Adds a new action to be executed at some time in the future (or perhaps
  # the past).
  def add_relative(self, duration, target, arg=None):
    return self.add_absolute(clock.get_time() + duration, target, arg)

  # Figures out the first (lowest) timestamp for which some action is scheduled.
  def _first_time(self):
    a = self.schedule.keys() + self.thread_map.values()
    assert len(a) > 0
    return sorted(a)[0]

  # This is the scheduler's main loop. It runs for a specific amount of
  # time and then exits (even if there are still actions to be executed).
  def loop(self, duration):
    gauge = Gauge.get('scheduler.latency')
    gauge.start_timer()
    until = duration + clock.get_time()

    # Runs for the specified amount of time.
    while clock.get_time() < until:
      # Figures out when the first scheduled action is and advances the clock
      # until then.
      now = clock.get_time()
      t = min(self._first_time(), until)
      clock.set_time(t)

      # If there are scheduled items to be done right now, do them.
      if t in self.schedule:
        # Makes a copy of the list of actions that we need to execute and clears
        # out the schedule for this timestamp. This will allow actions to schedule
        # something for the current time.
        actions = list(self.schedule[t])
        del self.schedule[t]

        # Executes all the actions we need to execute now.
        for task in actions:
          if type(task[0]) == types.LambdaType:
            task[0]()
          else:
            task[0].execute(task[1])

      # And continue any threads which need to continue.
      for (thread, timestamp) in self.thread_map.iteritems():
        if timestamp <= t:
          self.update_thread(thread, thread.thread_continue())

    # Stop the timer
    gauge.stop_timer()

    # Runs the finalizers.
    logger.info('Running finalizers')

    for target in self.finalizers:
      if type(target) == types.LambdaType:
        target()
      else:
        target.finalize()

    # Ends the simulation.
    logger.info('End of simulation. It took %lf seconds' % gauge.get_value())


# Exports a singleton global scheduler object.
scheduler = Scheduler()
