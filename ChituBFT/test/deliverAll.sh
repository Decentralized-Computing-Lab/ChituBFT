#!/bin/bash

NUM=$1

for(( i = 0 ; i < NUM ; i++)); do

        {

        host1=$(jq  '.nodes['$i'].host'  nodes.json)

        host=${host1//\"/}

        port1=$(jq  '.nodes['$i'].port'  nodes.json)

        port=${port1//\"/}

        user1=$(jq  '.nodes['$i'].user' nodes.json)

        user=${user1//\"/}

        key1=$(jq  '.nodes['$i'].keypath' nodes.json)

        key=${key1//\"/}

        id1=$(jq  '.nodes['$i'].id'  nodes.json)

        id=${id1//\"/}

        node="node"$id


	expect <<-END

        spawn ssh -oStrictHostKeyChecking=no -i $key $user@$host -p $port "cd;mkdir chitu;mkdir -p chitu/conf;mkdir -p chitu/remote"
          
        expect EOF

        exit
        
	END
	

       expect -c "

        set timeout -1

        spawn scp -i $key ../chitu  $user@$host:chitu/

        expect 100%

        exit

       "

	expect -c "
       
        set timeout -1

        spawn scp -i $key ./close_p.sh $user@$host:chitu/remote/

        expect 100%

        exit
       "

	expect -c "

        set timeout -1
        
        spawn scp -i $key ../conf/$node.json $user@$host:chitu/conf/

        expect 100%

	exit

       "

        expect -c "
       
        set timeout -1

        spawn scp -i $key ./mod.sh $user@$host:chitu/remote/

        expect 100%

        exit
       "

        expect -c "

        set timeout -1

        spawn scp -i $key ./mod $user@$host:chitu/

        expect 100%

        exit

       "

       expect -c "

        set timeout -1

        spawn scp -i $key ../client/client $user@$host:chitu/

        expect 100%

        exit

       "

        } & 
done

wait



