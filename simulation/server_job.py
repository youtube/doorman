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

from server import Server


# ServerJob mimicks a Borg job with a number of tasks. It creates
# the required number of Server (task) instances and provides
# access to them. It also implements some methods that affect
# the entire job, such as triggering a master election.


class ServerJob(object):
  all_server_jobs_list = list()

  def __init__(self, job_name, level, size, downstream_job=None):
    self.size = size
    self.tasks = dict()
    self.master = None
    self.job_name = job_name

    for i in range(0, size):
      s = Server(self, job_name, level, downstream_job)
      self.tasks[s.get_server_id()] = s

    self.trigger_master_election()
    ServerJob.all_server_jobs_list.append(self)

  # Iterator over all server jobs. Used for gathering reporting stats.
  @classmethod
  def all_server_jobs(self):
    for job in self.all_server_jobs_list:
      yield job

  # Returns a random job.
  @classmethod
  def get_random_server_job(self):
    return self.all_server_jobs_list[random.randint(0, len(self.all_server_jobs_list) - 1)]

  def get_job_name(self):
    return self.job_name

  # Returns the current master in the job.
  def get_master(self):
    return self.master

  # Returns a task by name.
  def get_task_by_name(self, name):
    return self.tasks[name]

  # Returns a random task. Used by a client to find a server to
  # send the Discovery RPC to. Mimicks a Stubby BNS channel.
  def get_random_task(self):
    return self.tasks.values()[random.randint(0, self.size - 1)]

  # Instructs the job to lose the current master. No new master
  # will be elected. Used to simulate an event where the master
  # goes away and electing a new master takes some time.
  def lose_master(self):
    self.master.lose_mastership()
    self.master = None

  # Triggers a master election. A new master is randomly selected
  # from the tasks in the job.
  # Note: The old master might stay the master.
  def trigger_master_election(self):
    old_master = self.master
    self.master = self.get_random_task()

    # Nothing to be done. The intended master is already the master.
    if old_master is self.master:
      assert self.master.is_master()
      return

    if old_master:
      old_master.lose_mastership()

    self.master.become_master()
