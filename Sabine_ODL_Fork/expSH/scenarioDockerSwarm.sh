#!/bin/bash

Help() {
  # Display Help
  echo "Add description of the script functions here."
  echo
  echo "Syntax: scriptTemplate [-g|h|v|V]"
  echo "options:"
  printf "\t h \t\t Print this Help.\n"
  printf "\t n int \t\t Set the number of node (default %d).\n" $NbNode
  printf "\t v int \t\t Set the number of validator (default %d).\n" $((NbNode-reduc))
  printf "\t m int \t\t Set a fix emitted throughput during %d s.\n" $delay
  printf "\t d int \t\t Set a simulated delay (default %d).\n" $lag
  printf "\t s \t\t Set a random delay scenario during.\n"
  printf "\t c int \t\t Set the control loop.\n"
  printf "\t p string \t Set a salt used fo multiple experiment.\n"
  echo
}

EXPDIR="./exp/scenario"
bootIP="5000"
bootAddr="localhost"
model="modelPI.csv"
NbNode=50
#ModelFile="/home/model/$model"
shPath=$(dirname "$0")

#scenario="50:600 40:600 60:600 30:1000 30:1000 30:1000 0:600"
scenario="25:900"

#scenario="30:180"

delayScenario="0:30 0:300 20:300 2:300"

reduc=33
nbPI=10

lag=0
controle=0
multisave=1
delay=310

fileName=docker-compose-test-swarm.yml
FCB=""
lagOpt=""
save=""
delayScenarioComm=""

while getopts "hn:v:m:d:p:cs " opt; do
  case $opt in
  h)
    Help
    exit 0
    ;;
  n)
    NbNode=$OPTARG
    ;;
  v)
    reduc=$((NbNode-OPTARG))
    ;;
  m)
    scenario="$OPTARG:$delay"
    ;;
  ' ')
    ;;
  d)
    lag=$OPTARG
    ;;
  c)
    controle=1
    ;;
  p)
    salt="$OPTARG"
    EXPDIR="$EXPDIR""$salt"
    fileName="${fileName%.*}""$salt"".yaml"
    ;;
  s)
    delayScenario=$(python "$shPath/generateScenarioDelaySmart.py" -s "$scenario" -d 15 )
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

LOGDIR="$EXPDIR"/logs/
CHAINDIR="$EXPDIR"/chain/
METRICDIR="$EXPDIR"/metric/
mkdir -p "$LOGDIR"
mkdir -p "$CHAINDIR"
mkdir -p "$METRICDIR"

resume="NbNode=$NbNode
lag=$lag
scenario=$scenario
reduc=$reduc
model=$model
nbPI=$nbPI
controle=$controle
delayScenario=$delayScenario
"

if [ $controle == 1 ]; then
  FCB="-f"
fi

if [ $multisave == 1 ]; then
  save="-s"
fi

if [ $lag -gt 0 ]; then
  lagOpt="-l $lag"
fi
if [ "$delayScenario" != "" ]; then
  delayScenarioComm="-d \"$delayScenario\""
fi
if [ "$model" != "" ]; then
  modelParam="-m $model"
fi

echo "$resume" >"$EXPDIR"/scenario.txt

param="$FCB $save $modelParam $lagOpt -F $fileName $NbNode $nbPI"
"$shPath/composeGen_swarm.sh" $(echo "$param")

parallel-ssh -h /etc/ssh/pssh_host/pssh -I <expSH/restoreExp.sh
docker stack deploy --compose-file $fileName stackpbft

sleep=$(bc <<<"scale=2; $NbNode *0.5 +.1")
sleep $sleep

echo debut
./pbftnode zombie throughput "$bootAddr:$bootIP" 2 $reduc $scenario --debug error -N $NbNode "$delayScenarioComm"
echo fin

docker stack rm stackpbft
sleep $sleep
echo fin de l\'expérience

listPID=""
for i in $(seq 1 $nbPI); do
  scp -Cr "ubuntu@3.14.15.$i:/exp/scenario/*" $EXPDIR &
  listPID="$listPID $!"
done

for PID in $listPID; do
  wait "$PID"
done

if [ $multisave == 1 ]; then
  python "$shPath/smartCheckAndConcat.py" $CHAINDIR
fi

refChain=$(du -bs $CHAINDIR/* | sort -rn | head -n 1 | awk '{print $2;}')
refChain=$(basename "$refChain")
find $CHAINDIR ! -name "$refChain" -type f -delete
mv "$CHAINDIR/$refChain" "$CHAINDIR/chain"
rm "$fileName"

echo fin des copies
