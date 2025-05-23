#!/bin/sh

EXPDIR="exp/scenario"
bootIP="4315"
bootAddr="localhost"
NbNode=16
lag=0
#scenario="180:180 250:180 200:180"
scenario="180:600"
reduc=0
log="warn"

./pbftnode bootstrap --debug error $bootIP &
bootPID=$!

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
METRICDIR="$EXPDIR"/metric/
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"
mkdir -p "$METRICDIR"
listPID=""
calc=$((NbNode - 1))

resume="NbNode=$NbNode
lag=$lag
scenario=$scenario
reduc=$reduc
log=$log
"
echo "$resume" > "$EXPDIR"/scenario.txt

i=0
./pbftnode node "$bootAddr:$bootIP" "$i" --debug debug --logFile "$LOGDIR"/log_"$i" -N $NbNode --PoA --chainfile "$CHAINDIR"/chain_"$i" --RamOpt --PoissonParam $lag --metricSaveFile "$METRICDIR"/metric_0 &
listPID="$listPID $!"
sleep 0.1

for i in $(seq 1 $calc); do
  ./pbftnode node "$bootAddr:$bootIP" "$i" --debug "$log" --logFile "$LOGDIR"/log_"$i" -N $NbNode --PoA --chainfile "$CHAINDIR"/chain_"$i" --RamOpt --PoissonParam $lag &
  listPID="$listPID $!"
  sleep 0.1
done
sleep 0.1
./pbftnode zombie throughput -b "$bootAddr:$bootIP" 2 $reduc $scenario --debug error

sleep 0.1
echo $listPID

for i in $listPID; do
  kill -2 "$i"
  wait "$i"
done
kill $bootPID
