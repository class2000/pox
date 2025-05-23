#!/bin/bash

EXPDIR="./exp/"
lag=0

nbArg=2

if [ $# == "$nbArg" ]; then
  EXPDIR=$1
  lag=$2
fi

bootIP="5000"
bootAddr="localhost"
NbNode=50

#scenario="40:180 35:180 30:180 25:180 20:180 15:180"
scenario="30:4000"
delay=600
#scenario="42:36000"
log="warn"
nbPI=10
step=3

nbLoop=1

max_val_capa=23.85
min_val_capa=105.16

fileName=docker-compose-test-swarm.yml

EXPDIRTEMP="$EXPDIR/exp_temp/"
LOGDIR="$EXPDIRTEMP"/logs/
CHAINDIR="$EXPDIRTEMP"/chain/
METRICDIR="$EXPDIRTEMP"/metric/

REFCHAIN="$EXPDIR/chains/"
REFLOG="$EXPDIR/logs/"
REFMET="$EXPDIR/metric/"

mkdir -p "$REFCHAIN"
mkdir -p "$REFLOG"
mkdir -p "$REFMET"

if [ $delay -eq 0 ]; then
  resume="
Balayage
NbNode=$NbNode
lag=$lag
scenario=$scenario
log=$log
nbPI=$nbPI
nbLoop=$nbLoop
"
else
  resume="
Balayage
NbNode=$NbNode
lag=$lag
scenario=lineaire from $min_val_capa to $max_val_capa
log=$log
nbPI=$nbPI
nbLoop=$nbLoop
"
fi
echo "$resume" >"$EXPDIR"/scenario.txt

liste=$(seq 4 $step $NbNode)
#liste="37 41 49"

for loop in $(seq 1 $nbLoop); do
  for nbVal in $liste; do
    echo "Expérience $nbVal/$NbNode"
    mkdir -p "$LOGDIR"
    mkdir -p "$CHAINDIR"
    mkdir -p "$METRICDIR"

    parallel-ssh -h /etc/ssh/pssh_host/pssh -I <expSH/restoreExp.sh

    reduc=$((NbNode - nbVal))
    shPath=$(dirname "$0")
    "$shPath/composeGen_swarm.sh" $fileName $NbNode $nbPI 0 $lag
    docker stack deploy --compose-file $fileName stackpbft

    if [ $delay -ne 0 ]; then
      debit=$(python "$shPath"/calcDebit.py $NbNode $max_val_capa $min_val_capa "$nbVal")
      scenario="$debit:$delay"
    fi

    sleep=$(bc <<<"scale=2; $NbNode *0.5 +.1")
    sleep "$sleep"

    echo debut
    echo "scenario : $scenario"
    ./pbftnode zombie throughput "$bootAddr:$bootIP" 2 $reduc $scenario --debug error
    echo fin

    docker stack rm stackpbft
    sleep $sleep
    echo fin de l\'expérience

    for i in $(seq 1 $nbPI); do
      scp -Cr "ubuntu@3.14.15.$i:/exp/scenario/*" $EXPDIRTEMP
    done
    echo fin des copies

    endFile=""
    if [ $nbLoop -gt 1 ]; then
      endFile="_$loop"
    fi

    refChain=$(du -bs $CHAINDIR/* | sort -rn | head -n 1 | awk '{print $2;}')
    cp "$refChain" "$REFCHAIN/chain_""$nbVal""_""$NbNode""$endFile"
    refLog=$(du -bs $LOGDIR/* | sort -rn | head -n 1 | awk '{print $2;}')
    cp "$refLog" "$REFLOG/chain_""$nbVal""_""$NbNode""$endFile"
    #    refMet=$(du -bs $METRICDIR/* | sort -rn | head -n 1 | awk '{print $2;}')
    #    cp "$refMet" "$REFMET/chain_""$nbVal""_""$NbNode""$endFile"
    rm -r "$EXPDIRTEMP"

  done
done
