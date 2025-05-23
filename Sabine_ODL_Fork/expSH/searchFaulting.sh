#!/bin/sh

repo=$1
if [ ! -d "$repo" ]; then
  echo "$repo" is not a valid repo
  exit 0
elif [ ! -d "$repo"/chain ] || [ ! -d "$repo"/logs ]; then
  echo "The log or chain repo doesn't exists"
  exit 0
fi

list_exp_chain=$(ls "$repo"/chain)
for chains in $list_exp_chain; do
  refNbBloc=0
  chains="$repo"/chain/"$chains"
  list_chain=$(ls $chains)
  for chain in $list_chain; do
    chain="$chains"/"$chain"
    nbBLoc=$(grep -c "sequence_nb" "$chain")
    if [ $refNbBloc = 0 ]; then
      refNbBloc=$nbBLoc
    elif [ $nbBLoc -lt $refNbBloc ]; then
      echo "Error on $chain, $nbBLoc instead of $refNbBloc"
    elif [ $nbBLoc -gt $refNbBloc ]; then
      refNbBloc=$nbBLoc
      echo "Error before $chain, $refNbBloc instead of $nbBLoc"
    fi
  done
done
