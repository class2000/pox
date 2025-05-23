#!/bin/sh

typeExp=throughput
nbtransac=250
EXPDIR="exp"
bootIP="4315"
bootAddr="localhost"
list_latency="0 1 2 4 8 16 32 64 128 256"
#list_latency="0 1 3 4 5 6 7 8 9 10 12 14 16 18 20 25 30 35 40 45 50 60 70 80 90 100 125 150"
NbNode=32
init=4
duration=120

for lag in $list_latency; do
  lag_repo="$EXPDIR"/tx_latence/lag_"$lag"
  mkdir -p "$lag_repo"

  for nbVal in $(seq $init $NbNode); do
    ./pbftnode bootstrap $bootIP --debug error &
    bootPID=$!

    LOGDIR="$EXPDIR"/"$typeExp"/logs/log_"$nbVal"_"$NbNode"
    CHAINDIR="$EXPDIR"/"$typeExp"/chain/chain_"$nbVal"_"$NbNode"
    mkdir -p "$LOGDIR"
    mkdir -p "$CHAINDIR"
    listPID=""
    calc=$((NbNode - 1))

    for i in $(seq 0 $calc); do
      ./pbftnode node "$bootAddr:$bootIP" "$i" --debug error --logFile "$LOGDIR"/log_"$i" -N $NbNode --PoA --chainfile "$CHAINDIR"/chain_"$i" --PoissonParam "$lag" --RamOpt &
      listPID="$listPID $!"
      sleep 0.1
    done
    sleep 0.1
    reduc=$((NbNode - nbVal))
    echo experience "$nbVal"/"$NbNode"

    ./pbftnode zombie throughput -b "$bootAddr:$bootIP" 2 $reduc "$nbtransac":"$duration" --logFile "$LOGDIR"/log_zombie --debug error

    for i in $listPID; do
      kill -2 "$i"
      wait "$i"
    done
    kill $bootPID
    ref=$(du -ab "$CHAINDIR" | sort -n -r | head -n 2 | tail -n 1 | awk '{print $2;}')
    cp "$ref" "$lag_repo"/chain_"$nbVal"_"$NbNode"
  done

done
