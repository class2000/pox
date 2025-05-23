#!/bin/bash

fileName=docker-compose-test-swarm.yml
NumberOfNode=8
CPUperNode="0.5"
MemLim="1024M"
MemReserv="384M"
log="warn"
nbPi=4
regularSave=10
txPoolBehavior="Drop"
controPeriod=24
refreshPeriod=5
SondeNumber=2
MetricTicker=5
typeLag="Fix"
ModelFile="/home/model/3Dmap.csv"

lag=""
ModelType=""
FCB=""
modelFileOpt=""
save=""

nbArg=2

Help() {
  # Display Help
  echo "Create a yaml file use in the docker swarm."
  echo
  echo "Syntax: composeGen_swarm [options] NumberOfNode nbPi"
  echo "options:"
  printf "\t h  \t\t Print this Help. \n"
  printf "\t c float  \t Set the cpu per node. \n"
  printf "\t m string \t Set the model. \n"
  printf "\t s \t\t Save in multiple file \n"
  printf "\t f \t\t Set the Feedback Control \n"
  printf "\t l int \t\t Set the lag \n"
  printf "\t g string \t Delay type (if lag) {NoDelay|Normal|Poisson|Fix} (default %s) \n" $typeLag
  printf "\t T string \t Set the FCB type {OneValidator|Hysteresis|ModelComparison} (default ModelComparison) \n"
  printf "\t m string \t Set the modelFile (default %s) \n" $ModelFile
  printf "\t F string \t Yaml filename (default %s) \n" $fileName
  echo
}

while getopts "hfg:l:c:sF:m:T: " opt; do
  case $opt in
  h)
    Help
    exit 0
    ;;
  f)
    FCB="--FCB"
    ;;
  g)
    typeLag="$OPTARG"
    ;;
  l)
    lag="--avgDelay $OPTARG"
    ;;
  c)
    CPUperNode=$OPTARG
    ;;
  s)
    save="--multiSaveFile"
    ;;
  F)
    fileName=$OPTARG
    ;;
  T)
    ModelType="--FCType $OPTARG"
    ;;
  m)
    ModelFile=$OPTARG
    if [ "$ModelFile" != "" ]; then
      modelFileOpt="--modelFile /home/model/$ModelFile"
    fi
    ;;
  ' ')
    ;;
  \?)
    echo "Invalid option: -$OPTARG l" >&2
    exit 1
    ;;
  :)
    echo "Option -$OPTARG requires an argument." >&2
    exit 1
    ;;
  esac
done
shift $((OPTIND - 1))

typeLag="--delayType $typeLag"
Flag="--RamOpt --PoA --txPoolBehavior $txPoolBehavior --debug $log --regularSave $regularSave $typeLag $lag $ModelType --ControlPeriod $controPeriod --RefreshingPeriod $refreshPeriod $FCB $modelFileOpt $save"

if [ $# == "$nbArg" ]; then
  NumberOfNode=$1
  nbPi=$2
else
  echo "Error in the number of argument, need $nbArg, got $#" >&2
  Help
  exit 1
fi

echo "version: '3.9'

services:
  bootstrap:
    container_name: bootstrap
    image: guilain/pbftnode
    environment:
      BootPort: 4315
    deploy:
      placement:
        constraints:
          - 'node.role==manager'
    command: pbftnode bootstrap --debug trace 4315
" >$fileName

echo "

  dealer:
    container_name: dealer
    image: guilain/pbftnode
    environment:
      IncomePort: 5000
    ports:
          - 5000:5000
    deploy:
      placement:
        constraints:
          - 'node.role==manager'
    command: pbftnode dealer bootstrap:4315 -N $NumberOfNode
" >>$fileName

echo "

  node0:
    container_name: node0
    image: guilain/pbftnode_armv8
    depends_on:
      - bootstrap
    ports:
      - 6060:6060
    volumes:
      - /exp/scenario/chain:/home/chain
      - /exp/scenario/logs:/home/logs
      - /home/ubuntu/model:/home/model
    deploy:
      restart_policy:
        condition: none
      placement:
        constraints:
          - 'node.hostname==pi1'
      resources:
        limits:
          cpus: '$CPUperNode'
          memory: '$MemLim'
        reservations:
          cpus: '$CPUperNode'
          memory: '$MemReserv'
    stop_signal: SIGINT
    command: pbftnode node bootstrap:4315 0 --chainfile /home/chain/chain_0 --logFile /home/logs/log_0 -N $NumberOfNode $Flag" >>$fileName
#--metricSaveFile /home/metric/metric_0

for i in $(seq 1 $SondeNumber); do
  PiId=$((i % nbPi + 1))

  echo "

  node$i:
    container_name: node$i
    image: guilain/pbftnode_armv8
    depends_on:
      - bootstrap
    volumes:
      - /exp/scenario/chain:/home/chain
      - /exp/scenario/logs:/home/logs
      - /exp/scenario/metric:/home/metric
      - /home/ubuntu/model:/home/model
    deploy:
      restart_policy:
        condition: none
      placement:
        constraints:
          - 'node.hostname==pi$PiId'
      resources:
        limits:
          cpus: '$CPUperNode'
          memory: '$MemLim'
        reservations:
          cpus: '$CPUperNode'
          memory: '$MemReserv'
    stop_signal: SIGINT
    command: pbftnode node bootstrap:4315 $i --chainfile /home/chain/chain_$i --logFile /home/logs/log_$i -N $NumberOfNode $Flag --metricSaveFile /home/metric/metric_$i --metricTicker $MetricTicker" >>$fileName
done

MinimumNb=$((SondeNumber + 1))
NumberOfNodeLess=$((NumberOfNode - 1))
for i in $(seq $MinimumNb $NumberOfNodeLess); do
  PiId=$((i % nbPi + 1))
  echo "

  node$i:
    container_name: node$i
    image: guilain/pbftnode_armv8:latest
    depends_on:
      - bootstrap
    volumes:
      - /exp/scenario/chain:/home/chain
      - /exp/scenario/logs:/home/logs
      - /home/ubuntu/model:/home/model
    deploy:
      restart_policy:
        condition: none
      placement:
        constraints:
          - 'node.hostname==pi$PiId'
      resources:
        limits:
          cpus: '$CPUperNode'
          memory: '$MemLim'
        reservations:
          cpus: '$CPUperNode'
          memory: '$MemReserv'
    stop_signal: SIGINT
    command: pbftnode node bootstrap:4315 $i --chainfile /home/chain/chain_$i --logFile /home/logs/log_$i -N $NumberOfNode $Flag " >>$fileName
done
