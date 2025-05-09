#!/bin/bash

NODE=$1

BATCHSIZE=$2

PAYLOAD=$3

TIME=$4

SLICE=$5

IDENTITY=$6

ATTACKSIM=$7
    
cd ..

rm *.log

chmod +x mod

./mod --size=$BATCHSIZE --payload=$PAYLOAD --node=$NODE --time=$TIME

./chitu -c ./conf/$NODE.json -n $SLICE -b $IDENTITY -a $ATTACKSIM $ >$NODE.log 2>&1

sleep 100

