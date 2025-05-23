#!/bin/bash

EXPDIR="./exp/scenario"
bootIP="5000"
bootAddr="localhost"
NbNode=16
lag=0
throughput="100"
DelayPerChange="90"
NbValPerChange="1"
reduc=0
log="warn"
nbPI=10

fileName=docker-compose-test.yml
CPUperNode="0.5"
MemLim="625m"

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
CHAINDIR="$EXPDIR"/metric/
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"

resume="Running Swarm
NbNode=$NbNode
lag=$lag
throughput=$throughput
DelayPerChange=$DelayPerChange
NbValPerChange=$NbValPerChange
reduc=$reduc
log=$log
nbPI=$nbPI
"
echo "$resume" > "$EXPDIR"/scenario.txt

shPath=$(dirname "$0")
"$shPath/composeGen_swarm.sh" $fileName $NbNode $CPUperNode $MemLim $nbPI
docker stack deploy --compose-file $fileName stackpbft

sleep=$(bc <<< "scale=2; $NbNode *0.5 +.1")
sleep $sleep

echo debut
./pbftnode zombie running "$bootAddr:$bootIP" 2 $reduc $NbNode $throughput $DelayPerChange $NbValPerChange
echo fin

docker stack rm stackpbft
sleep $sleep
echo fin de l\'expérience

for i in $(seq 1 $nbPI); do
  scp -r "ubuntu@3.14.15.$i:/exp/scenario/*" $EXPDIR
done
echo fin des copies
