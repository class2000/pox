#!/bin/bash
fileName=docker-compose-test-swarm.yml
NumberOfNode=8
CPUperNode="0.3"
MemLim="625m"
scenario="./exp/scenario"
modelDir="./models"
lag=""
log="error"
regularSave=55
txPoolBehavior="Drop"
controPeriod=12
refreshPeriod=1
SondeNumber=2
MetricTicker=5
typeLag="Fix"
ModelFile="/home/model/model.csv"
FCB=""

nbArg=4

if [ $# == "$nbArg" ]; then
  fileName=$1
  scenario=$2
  NumberOfNode=$3
  lag=""
  scenario=$3
  if [ $3 == "FCB" ]; then
    FCB="--FCB"
  fi
elif [ $# == 5 ]; then
  fileName=$1
  scenario=$2
  NumberOfNode=$3
  if [ $4 == "FCB" ]; then
    FCB="--FCB"
  fi
  lag="--delayType $typeLag --avgDelay $5"
else
  echo "composeGen_swarm.sh [fileName] [repo] [NumberOfNode] [FCB] [lag ?]"
fi

unusedFlag="--multiSaveFile"
Flag="--RamOpt --PoA --txPoolBehavior $txPoolBehavior --debug $log --regularSave $regularSave $lag --modelFile $ModelFile --FCType ModelComparison --ControlPeriod $controPeriod --RefreshingPeriod $refreshPeriod $FCB"


echo "version: '2.4'

services:
  bootstrap:
    container_name: bootstrap
    image: guilain/pbftnode
    environment:
      BootPort: 4315
    command: pbftnode bootstrap --debug trace 4315
" >$fileName

NumberOfNodeLess=$((NumberOfNode - 1))
echo $NumberOfNode
echo "

  dealer:
    container_name: dealer
    image: guilain/pbftnode
    depends_on:" >>$fileName

for i in $(seq 0 $NumberOfNodeLess); do
echo   "      - node$i">>$fileName
done

echo "    environment:
      IncomePort: 5000
    ports:
      - 5000:5000
    command: pbftnode dealer bootstrap:4315 -N $NumberOfNode --debug trace
" >>$fileName

for i in $(seq 0 $SondeNumber); do
  sleep=$((i * 50 + 100))
  echo "

  node$i:
    container_name: node$i
    image: guilain/pbftnode
    depends_on:
      - bootstrap
    volumes:
      - $scenario/chain:/home/chain
      - $scenario/logs:/home/logs
      - $scenario:/home/metric
      - $modelDir/model:/home/model
    stop_signal: SIGINT
    cpus: $CPUperNode
    mem_limit: $MemLim
    command: pbftnode node bootstrap:4315 $i --logFile /home/logs/log_$i --chainfile /home/chain/chain_$i -N $NumberOfNode $Flag --metricSaveFile /home/metric/metric_$i --metricTicker $MetricTicker " >>$fileName
done

MinimumNb=$((SondeNumber + 1))
for i in $(seq $MinimumNb $NumberOfNodeLess); do
  sleep=$((i * 50 + 100))
  echo "

  node$i:
    container_name: node$i
    image: guilain/pbftnode
    depends_on:
      - bootstrap
    volumes:
      - $scenario/chain:/home/chain
      - $scenario/logs:/home/logs
      - $modelDir/model:/home/model
    stop_signal: SIGINT
    cpus: $CPUperNode
    mem_limit: $MemLim
    command: pbftnode node bootstrap:4315 $i --logFile /home/logs/log_$i -N $NumberOfNode $Flag " >>$fileName
done
