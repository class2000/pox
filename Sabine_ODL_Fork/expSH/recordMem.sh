#!/bin/sh

free -h |grep total > memory.log
for i in $(seq 0 $1)
do
        free |grep Mem: >> memory.log
        sleep 10
done