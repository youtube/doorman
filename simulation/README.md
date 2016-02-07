# Doorman Simulation in Python

Author: @josvisser

This directory contains a simulation of the Doorman system in Python. This simulation precedes the actual implementations of the Doorman server and clients, and was used to verify the ideas in the design doc. It is still usable today to check quickly and easily how Doorman would behave in a particular scenario. The simulation runs on a simulated clock, so you can check hours of runs in seconds (depending on the speed of your computer :-)

To run the simulation you require Python2.7.x and Google protocol buffers 2.6.x. Personally I use the system provided Python on my MacOs El Capitan laptop and a HomeBrew installed version of protobuf260.

The protocol buffer files are used to define the client-server RPC protocol and to describe the state maintained by the client and the server, as per the proposal for that state in the design doc. This is suboptimal from a performance perspective, but it shows that the ideas in the design doc w.r.t. the state clients and servers should maintain are valid.

Each scenario in the simulation creates a tree of Doorman servers and then creates one or more clients that ask Doorman for capacity for a resource called "resource0". The tree of servers is modelled after Google's internal cloud computing platform, with regions, datacenters, jobs and tasks within that job. There is also a simulated master election process that assigns a winner from among the server tasks in a particular node in the tree.

You can run each scenario independently (e.g. "./scenario_one.py") or run all the scenarios in one firm go use "./run_all_scenarios.sh". At the end of every scenario it writes out a CSV file with the results of the simulation. These can be imported into your favorite spreadsheet and then analyzed and graphed. Creating new scenarios is easy; just use the existing ones for inspiration.

A "Makefile" is provided to generate the Python implementation for the protocol buffers.