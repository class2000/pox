#!/bin/sh


nbtransac=100
EXPDIR="exp"
bootIP="4315"
bootAddr="localhost"
NbNode=32
init=30

for k in $(seq 0 100)
do
  for nbVal in $(seq $init $NbNode); do
    ./pbftnode bootstrap $bootIP --debug error &
    bootPID=$!

    LOGDIR="$EXPDIR"/logs/log_"$nbVal"_"$NbNode"
    CHAINDIR="$EXPDIR"/chain/chain_"$nbVal"_"$NbNode"
    mkdir -p "$LOGDIR"
    mkdir -p "$CHAINDIR"
    listPID=""
    calc=$((NbNode - 1))

    for i in $(seq 0 $calc); do
      ./pbftnode node "$bootAddr:$bootIP" "$i" --debug trace --logFile "$LOGDIR"/log_"$i" -N $NbNode --PoA --chainfile "$CHAINDIR"/chain_"$i" --avgDelay 50 --stdDelay 10 &
      listPID="$listPID $!"
      sleep 0.1
    done
    reduc=$((NbNode - nbVal))
    echo experience "$nbVal"/"$NbNode"
    ./pbftnode zombie latency -b "$bootAddr:$bootIP" 2 $reduc "$nbtransac" --logFile "$LOGDIR"/log_zombie --debug trace

    for i in $listPID; do
      kill -2 "$i"
    done
    kill $bootPID
  done
done


