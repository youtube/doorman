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

from global_config import global_config_proto


# This class wraps the global configuration object and adds some
# convenience methods for accessing the configuration.
class ConfigWrapper(object):

  def __init__(self, config):
    self.wrapped_config = config

  # Gets the default algorithm that is specified in the
  # configuration. Returns None if no default algorithm
  # has been configured.
  def get_default_algorithm(self):
    if self.wrapped_config.HasField('default_algorithm'):
      return self.wrapped_config.default_algorithm

    return None

  # Finds the resource template which applies to the given
  # resource id. Returns None if no template can be found.
  def find_resource_template(self, resource_id):
    # First check if there is a template with exactly the
    # name name as the resource id. If so, return that one.
    for t in self.wrapped_config.resource:
      if t.identifier_re == resource_id:
        return t

    # If not, try to find a template using regular expression
    # matching.
    for t in self.wrapped_config.resource:
      if re.match(t.identifier_re, resource_id):
        return t

    return None


# Exports a singleton instance of the ConfigWrapper which encapsulates
# the global configuration.
global_config = ConfigWrapper(global_config_proto)
