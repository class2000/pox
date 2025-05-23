#!/bin/bash

EXPDIR="./exp/scenario"
bootIP="5000"
bootAddr="localhost"
NbNode=16
lag=0
throughput="60"
DelayPerChange="60"
NbValPerChange="1"
reduc=0
log="error"

fileName=docker-compose-test.yml
CPUperNode="0.3"
MemLim="625m"

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
CHAINDIR="$EXPDIR"/metric/
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"

resume="Running
NbNode=$NbNode
lag=$lag
throughput=$throughput
DelayPerChange=$DelayPerChange
NbValPerChange=$NbValPerChange
reduc=$reduc
log=$log
"
echo "$resume" > "$EXPDIR"/scenario.txt

shPath=$(dirname "$0")
"$shPath/composeGen.sh" $fileName $NbNode $CPUperNode $MemLim $EXPDIR
docker-compose -f $fileName up -d

sleep=$(bc <<< "scale=2; $NbNode *0.5 +.1")
sleep $sleep

echo debut
./pbftnode zombie running "$bootAddr:$bootIP" 2 $reduc $NbNode $throughput $DelayPerChange $NbValPerChange
echo fin

docker-compose -f $fileName down
