#!/bin/sh

LOGDIR="log"
CHAINDIR="chain"
bootIP="4315"
bootAddr="localhost"
NbNode=7
log="trace"
lag=0
init=0

./pbftnode bootstrap $bootIP --debug trace &
bootPID=$!

sleep 1

mkdir -p $LOGDIR
mkdir -p $CHAINDIR

listPID=""
calc=$((NbNode-1))
for i in $(seq $init $calc) ; do
    ./pbftnode node "$bootAddr:$bootIP" $i --debug trace --logFile $LOGDIR/log_"$i" -N $NbNode --PoA --chainfile $CHAINDIR/chain_"$i" &
    # echo ./pbftnode node "$bootAddr:$bootIP" $i --debug trace --logFile $LOGDIR/log_"$i" -N $NbNode --PoA --chainfile $CHAINDIR/chain_"$i" --RamOpt
    listPID="$listPID $!"
    sleep 0.1
done

read -n 15 ICI
kill $bootPID
for i in $listPID ; do
    kill -2 "$i"
    wait "$i"
done
