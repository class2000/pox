#!/bin/bash

fullpath="$0"
path="${fullpath%/*}/"

listLatency="0 1 2 4 8 16 32"

for i in $listLatency; do
  bash "$path"launch_experiment_docker.sh "$i"
done
