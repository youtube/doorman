#!/bin/bash

for f in one two three four five six seven; do
  echo
  echo Running scenario $f
  echo
  eval ./scenario_$f.py 2>&1 | tee scenario_$f.txt
done
