#!/bin/sh

nbArg=3

if [ $# -gt "$nbArg" ] || [ "$1" != "latency" ] && [ "$1" != "throughput" ]; then
  echo "bash launch_experiment.sh [latency|throughput] [nbTx (per sec for throughput)] [network latency]"
  exit 1
fi


if [ $# = "$nbArg" ]; then
  lag=$3
  subRep="lag_$3/"
else
  subRep=""
  lag=0
fi

typeExp=$1
nbtransac=$2
EXPDIR="exp"
bootIP="4315"
bootAddr="localhost"
NbNode=32
init=4
duration=60

for nbVal in $(seq $init $NbNode); do
  ./pbftnode bootstrap $bootIP --debug error &
  bootPID=$!

  LOGDIR="$EXPDIR"/"$typeExp"/"$subRep"logs/log_"$nbVal"_"$NbNode"
  CHAINDIR="$EXPDIR"/"$typeExp"/"$subRep"chain/chain_"$nbVal"_"$NbNode"
  mkdir -p "$LOGDIR"
  mkdir -p "$CHAINDIR"
  listPID=""
  calc=$((NbNode - 1))

  for i in $(seq 0 $calc); do
    ./pbftnode node "$bootAddr:$bootIP" "$i" --debug error --logFile "$LOGDIR"/log_"$i" -N $NbNode --PoA --chainfile "$CHAINDIR"/chain_"$i" --PoissonParam $lag --RamOpt &
    listPID="$listPID $!"
    sleep 0.1
  done
  sleep 0.1
  reduc=$((NbNode - nbVal))
  echo experience "$nbVal"/"$NbNode"
  if [ "$typeExp" = "latency" ]; then
    ./pbftnode zombie latency -b "$bootAddr:$bootIP" 2 $reduc "$nbtransac" --logFile "$LOGDIR"/log_zombie --debug trace
  else
    ./pbftnode zombie throughput -b "$bootAddr:$bootIP" 2 $reduc "$nbtransac":"$duration" --logFile "$LOGDIR"/log_zombie --debug error
  fi

  for i in $listPID; do
    kill -2 "$i"
  done
  kill $bootPID
done

if [ -z "$subRep" ]; then
  zip -rm "$EXPDIR"/"$typeExp"/lag_"$3".zip "$EXPDIR"/"$typeExp"/"$subRep"
fi
