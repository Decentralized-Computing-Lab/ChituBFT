# Chitu
This is an implementation of `ChituBFT: Avoiding Unnecessary Fallback in Byzantine Consensus`.

## Files
```
.
|- Makefile
|- README.md    # Introduction for the implementation of the protocol
|
|- coordinator  # coordinator: control when test begins and gather results when test ends
|
|- client       # client: send request to node
|
|- main.go      # build consensus node
|
|- consensus    # core of protocol
|- network      # network layer
|- proto        # protobuf primary definition
|- common       # common struct definition and message protobuf file
|- crypto       # crypto primitives: Hash, Threshold Signature, Signature
|- logger       # record running info
|- utils        # utilized data structure
|
|- test         # scripts for deliver, start and stop nodes and clients, and download logs
```

## Environment Setup
* Golang 1.16+

* install `expect`, `jq`
  ```
  $ sudo apt update
  $ sudo apt install -y expect jq
  ```

* python3+
  ```bash
  # boto3 offers AWS APIs which we can use to access the service of AWS in a shell 
  $ pip3 install boto3==1.16.0
  $ pip3 install numpy
  ```

* protobuf, the implementation uses protobuf to serialize messages, refer to [link](https://github.com/gogo/protobuf).  

    install protoc-gen-gogofaster in your $PATH for the protocol buffer compiler to find it.
    ```bash
    $ go install github.com/gogo/protobuf/protoc-gen-gofast
    ```

    We provide a protobuf file compiled in Ubuntu 22.04 x86 platform, see [message.pb.go](./common/message.pb.go)

## Test

### Build ChituBFT
You can build node, client and coordinator directly by
```bash
$ make
```

### Run ChituBFT on a cluster of AWS EC2 machines

1. **Create a cluster of AWS EC2 machines.**  
    
    In [AwsCli](../AwsCli/), you can set regions in [settings.json](../AwsCli/settings.json) and set the number of EC2 machines created in each region in [fabfile.py](../AwsCli/fabfile.py).   

    For example, in our four nodes test, we create four `t3.2xlarge` instances in region `us-east-2`, `ap-southeast-1`, `ap-northeast-1`, `eu-central-1` and open port `4000-11000` (also open in AWS security group).    

    Specially, in crash-fault test, there is no need to create instances for crash nodes.

    After setting, create machines by
    ```bash
    $ cd AwsCli
    $ fab create
    ```

2. **Fetch machine information from AWS.**  

    In `test/aws.py`,   
    - **`keypath` shoud be changed to the path of your own ssh key to access your AWS account** 
    - variable `regions` and `Filter` should be corresponding to your settings
    
    Open a new terminal and fetch instance information by
    ```bash
    $ cd ChituBFT
    $ mkdir -p config
    $ cd test

    # fetch instance information
    $ python3 aws.py [instance num]
    ```

3. **Generate config files (`node1.json`, `node2.json`, ...).**
    ```bash
    $ go build -o create
    $ ./create -n [total node num] -f [tolerant fault num] (-w [number of crash nodes with no config files])
    ```

4. **Deliver nodes and clients.**   
    ```bash
    $ chmod +x mod
    $ chmod +x *.sh
    
    # Deliver to every node that does not crash.
    $ ./deliverAll.sh [instance num]
    ```

5. **Run nodes.**
   ```
   $ ./nodes.sh [instance num] [max batch size] [payload (byte)] [test time (sec)] [payload slice num] [byzantine node num]
   ```

   For example, in our four nodes test, we run
   ```bash
   $ ./nodes.sh 4 30000 1000 30 1 0
   ```

6. **Run clients.**

   Open a new terminal, run
   ```bash
   $ cd ChituBFT/test
   $ ./clients.sh [instance num]
   ```

7. **Run coordinator and start test.**

   Open a new terminal, run
   ```bash
   $ cd ChituBFT/coordinator

   # -b, -p, -t should be the same as in step 5
   # increase client rate to saturate the system
   $ ./coor -b [max batch size] -p [payload] -t [test time] -i [client rate: req num per 50ms] | tee -a result.txt
   ```

8. **Wait for [test time] + 10s and coordinator prints test result.**

   The system needs to run 10s for warm-up. 
   For example, the following is one of the results in our four nodes test:
   ```
   $ ./coor -b 30000 -p 1000 -t 30 -i 200 | tee -a result.txt
   batch: 30000, payload: 1000, test time: 30, client reqs size: 200
   notify 4
   node 1 execution states: [202 0] 
   node 4 execution states: [203 0] 
   node 3 execution states: [203 0] 
   node 2 execution states: [203 0] 
   execution average latency: map[1:425 2:456 3:472 4:459]
   execution 95 latency: map[1:472 2:476 3:501 4:496]
   finish block number: map[1:159 2:158 3:158 4:159]
   execution req number: map[1:118000 2:117600 3:117200 4:117800]
   produce round number: map[1:204 2:206 3:206 4:205]
   total finished block number: 634
   total executed req number: 470600
   total full-execution latency: 453
   total full-execution throughput: 15686
   -------------------------------------------
   ```

9. **Stop nodes and clients and copy logs back.**
   
   Open a new terminal, run
   ```bash
   $ cd ChituBFT
   $ mkdir -p log

   $ cd test
   $ ./stop.sh [instance num]
   ```

## Network ports

* node --> node: 5000+
* coordinator --> client: 6000+
* client --> node: 7000+
* node --> coordinator: 9000
