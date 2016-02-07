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

from config_pb2 import *


# Contains the global configuration for the rate limiting system.
global_config_proto = ResourceRepository()

# Adds a default algorithm.
default_algorithm = Algorithm()
default_algorithm.name = 'Static'
default_algorithm.param.add()
default_algorithm.param[0].name = 'capacity'
default_algorithm.param[0].value = '100'

global_config_proto.default_algorithm.CopyFrom(default_algorithm)

# Creates a ProportionalShare algorithm.
prop_algorithm = Algorithm()
prop_algorithm.name = 'ProportionalShare'
prop_algorithm.param.add()
prop_algorithm.param[0].name = 'refresh_interval'
prop_algorithm.param[0].value = '8'

# Adds a descriptor for resource0.
t = global_config_proto.resource.add()
t.identifier_re = 'resource0'
t.capacity = 500
t.safe_capacity = 10
t.owner411 = 1
t.algorithm.CopyFrom(prop_algorithm)

assert global_config_proto.IsInitialized()
