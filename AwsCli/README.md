# Use AWS in commandline
> Before using scripts in this directory, you should refer [this](https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-configure.html#cli-configure-quickstart-creds) to get an access_key and a secert_access_key and store them in `~/.aws/credentials`. Besides, you should generate a pair RSA keys and add the public key in every region used in your test. In defalut, the key name is `Main` (settings.json)    
## Setup
```bash
sudo apt update
sudo apt install -y pip3-python
pip3 install -r requirements.txt 
```
## Basic Introduction
### Instance Information (settings.json)
* AWS EC2 type: (default) t3.2xlarge
* AWS EC2 instances regions: (default) "us-east-1", "us-west-1", "ap-northeast-1", "ap-southeast-2"
* Security Group: (default) 4000-9000 ports are open
### Instance Number
* The parameter `num` in `create` function (fabfile.py) means that it will create  `num` EC2 instance(s) in every region
* The Total Number is equal to `num` * number of regions

## Usage
* open a termimal in this directory.
* create `num` instances in every reigon
```
fab create
```
* destroy all instances
```
fab destroy
```
* stop all instances
```
fab stop
```
* start `max` instances at most in every region
```
fab start
```

## Note
The scripts in this directory are modified from the relevant code in [this repository](https://github.com/asonnino/narwhal/tree/master/benchmark)