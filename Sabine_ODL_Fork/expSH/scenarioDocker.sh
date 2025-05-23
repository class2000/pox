#!/bin/bash

EXPDIR="./exp/scenario"
bootIP="5000"
bootAddr="localhost"
NbNode=16
lag=0
controle=0
scenario="300:600"
reduc=12
log="error"

fileName=docker-compose-test.yml
CPUperNode="0.3"
MemLim="625m"

if [ $controle == 1 ];then
  FCB="FCB"
else
  FCB="noFCB"
fi

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
CHAINDIR="$EXPDIR"/metric/
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"

resume="NbNode=$NbNode
lag=$lag
scenario=$scenario
reduc=$reduc
log=$log
"
echo "$resume" > "$EXPDIR"/scenario.txt

shPath=$(dirname "$0")
"$shPath/composeGen.sh" $fileName $EXPDIR $NbNode $FCB $lag
docker-compose -f $fileName up -d

sleep=$(bc <<< "scale=2; $NbNode *0.5 +.1")
sleep $sleep

echo debut
./pbftnode zombie throughput "$bootAddr:$bootIP" 2 $reduc $scenario --debug error
echo fin

docker-compose -f $fileName down
