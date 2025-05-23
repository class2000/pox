#!/bin/bash

EXPDIR="./exp/scenario"
bootIP="5000"
bootAddr="localhost"
NbNode=16
lag=0
scenario="100:300"
log="debug"

nbArg=1

max_val_capa=23.85
min_val_capa=105.16
delay=600

if [ $# == "$nbArg" ]; then
  lag=$1
else
  lag=0
fi

fileName=docker-compose-test.yml

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
RESULTDIR="$EXPDIR/results/log_$lag"
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"
mkdir -p "$RESULTDIR"

resume="NbNode=$NbNode
lag=$lag
scenario=$scenario
nbVal=4 to $NbNode
log=$log
"

echo "$resume" >"$RESULTDIR"/scenario.txt
shPath=$(dirname "$0")

for nbVal in $(seq 4 $NbNode); do
  reduc=$((NbNode - nbVal))
  if [ $lag == 0 ]; then
    "$shPath/composeGen.sh"  $fileName $EXPDIR $NbNode "noFCB"
  else
    "$shPath/composeGen.sh"  $fileName $EXPDIR $NbNode "noFCB" $lag
  fi
  docker-compose -f $fileName up -d
  sleep=$(bc <<<"scale=2; $NbNode *0.5 +.1")
  sleep $sleep

  if [ $delay -ne 0 ]; then
    debit=$(python "$shPath"/calcDebit.py $NbNode $max_val_capa $min_val_capa "$nbVal")
    scenario="$debit:$delay"
  fi

  echo debut
  ./pbftnode zombie throughput "$bootAddr:$bootIP" 2 $reduc $scenario --debug error
  echo fin

  docker-compose -f $fileName down

  refChain=$(du -bs $CHAINDIR/* | sort -rn | head -n 1 | awk '{print $2;}')
  cp "$refChain" "$RESULTDIR/chain_""$nbVal""_""$NbNode"
done
