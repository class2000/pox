#!/bin/bash

fullpath="$0"
path="${fullpath%/*}/"

EXPDIR="./exp_latence_balai_fixe"
MODELDIR="$EXPDIR/models"
listLatency="0 32"


mkdir -p $MODELDIR
for i in $listLatency; do
  EXPDIRLAG="$EXPDIR/exps/lag_$i"
  mkdir -p "$EXPDIRLAG"
  bash "$path"launch_experiment_docker_swarm.sh "$EXPDIRLAG" "$i"
#  exit 0
  python "$path/capaAccordingNbVal.py" "$EXPDIRLAG/chains/" 600 "$MODELDIR/data_lag_$i.csv"
done
