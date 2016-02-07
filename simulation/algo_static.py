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

from algorithm import AlgorithmImpl


# Implements the "Static" algorithm. This algorithm gives everyone
# the same capacity, regardless of what they are asking for. The
# capacity that is handed out is configured by the "capacity"
# named parameter to the algorithm.
class StaticAlgorithm(AlgorithmImpl):

  def __init__(self, algo, server_level):
    self._get_default_parameters(algo)
    self.capacity = int(self.get_named_parameter(algo, 'capacity'))

    assert self.capacity > 0

  def run_client(self, resource, cr):
    self.create_lease(resource, cr, self.capacity)

  def run_server(self, resource, sr):
    self.run_client(resource, sr)
