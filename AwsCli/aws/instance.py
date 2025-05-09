# Copyright(C) Facebook, Inc. and its affiliates.
import boto3
from botocore.exceptions import ClientError
from collections import defaultdict, OrderedDict
from fabric import Connection, ThreadingGroup as Group
from fabric.exceptions import GroupException
from time import sleep

from aws.utils import Print, BenchError, progress_bar
from aws.settings import Settings, SettingsError
from aws.utils import FabricError, BenchError, ExecutionError


class AWSError(Exception):
    def __init__(self, error):
        assert isinstance(error, ClientError)
        self.message = error.response['Error']['Message']
        self.code = error.response['Error']['Code']
        super().__init__(self.message)


class InstanceManager:
    INSTANCE_NAME = 'Node'
    SECURITY_GROUP_NAME = 'GN'

    def __init__(self, settings):
        assert isinstance(settings, Settings)
        self.settings = settings
        self.clients = OrderedDict()
        for region in settings.aws_regions:
            self.clients[region] = boto3.client('ec2', region_name=region)
        ## add
        self.regions = settings.aws_regions
        self.public_ips = []
        self.private_ips = []
        self.fetch()

    def fetch(self):
        for region in self.regions:
            ec2 = boto3.client('ec2', region_name=region)
            response = ec2.describe_instances(Filters=[
                    {
                        'Name': 'tag:Name',
                        'Values': [self.INSTANCE_NAME]
                    },
                    {
                        'Name': 'instance-state-name',
                        'Values': ['running']
                    }
                ])
            for i in range(len(response['Reservations'])):
                instances = response['Reservations'][i]['Instances']   
                for instance in instances:
                    status = instance['State']['Name']
                    if status != "running":
                        continue
                    self.public_ips.append(instance['PublicIpAddress'])
                    self.private_ips.append(instance['PrivateIpAddress'])

    def excute_cmds_on_all_instances(self, cmds, hide = True):
        hosts = self.public_ips
        try:
            g = Group(*hosts, user='ubuntu', connect_kwargs=self.connect)
            ret_info = g.run(' && '.join(cmds), hide=hide)
            if not hide:
                return ret_info 
            print(f'Executed cmds on {len(hosts)} nodes')
        except (GroupException, ExecutionError) as e:
            e = FabricError(e) if isinstance(e, GroupException) else e
            raise BenchError('Failed to install repo on testbed', e)


    def excute_cmds_on_one_instance(self, node_id, cmds):
        try:
            c = Connection(self.public_ips[int(node_id)], user='ubuntu', connect_kwargs=self.connect)
            c.run(' && '.join(cmds))
           # return ret_info
        except (GroupException, ExecutionError) as e:
            raise BenchError('Failed to execute cmds on testbed', e)

    def put_files_on_all_instance(self, local_file, remote_dir):
        hosts = self.public_ips
        try:
            g = Group(*hosts, user='ubuntu', connect_kwargs=self.connect)
            ret_info = g.put(local_file, remote_dir)
            print(ret_info)
            print(f'send file to {len(hosts)} nodes')
        except (GroupException, ExecutionError) as e:
            e = FabricError(e) if isinstance(e, GroupException) else e
            raise BenchError('Failed to send files', e)

    def get_files_on_all_instance(self, local_dir, remote_file):
        hosts = self.public_ips
        try:
            g = Group(*hosts, user='ubuntu', connect_kwargs=self.connect)
            ret_info = g.get(local_dir, remote_file)
            print(ret_info)
            print(f'send file to {len(hosts)} nodes')
        except (GroupException, ExecutionError) as e:
            e = FabricError(e) if isinstance(e, GroupException) else e
            raise BenchError('Failed to send files', e)

    def get_files_from_one_instance(self, node_id, local_dir, remote_file):
        try:
            c = Connection(self.public_ips[int(node_id)], user='ubuntu', connect_kwargs=self.connect)
            ret_info = c.get(local_dir, remote_file)
           # return ret_info
        except (GroupException, ExecutionError) as e:
            raise BenchError(f'Failed to send files to node {node_id}', e)

    @classmethod
    def make(cls, settings_file='settings.json'):
        try:
            return cls(Settings.load(settings_file))
        except SettingsError as e:
            raise BenchError('Failed to load settings', e)

    def _get(self, state):
        # Possible states are: 'pending', 'running', 'shutting-down',
        # 'terminated', 'stopping', and 'stopped'.
        ids, ips = defaultdict(list), defaultdict(list)
        for region, client in self.clients.items():
            r = client.describe_instances(
                Filters=[
                    {
                        'Name': 'tag:Name',
                        'Values': [self.INSTANCE_NAME]
                    },
                    {
                        'Name': 'instance-state-name',
                        'Values': state
                    }
                ]
            )
            instances = [y for x in r['Reservations'] for y in x['Instances']]
            for x in instances:
                ids[region] += [x['InstanceId']]
                if 'PublicIpAddress' in x:
                    ips[region] += [x['PublicIpAddress']]
        return ids, ips

    def _wait(self, state):
        # Possible states are: 'pending', 'running', 'shutting-down',
        # 'terminated', 'stopping', and 'stopped'.
        while True:
            sleep(1)
            ids, _ = self._get(state)
            if sum(len(x) for x in ids.values()) == 0:
                break

    def _create_security_group(self, client):
        client.create_security_group(
            Description='HotStuff node',
            GroupName=self.SECURITY_GROUP_NAME,
        )

        client.authorize_security_group_ingress(
            GroupName=self.SECURITY_GROUP_NAME,
            IpPermissions=[
                {
                    'IpProtocol': 'tcp',
                    'FromPort': 22,
                    'ToPort': 22,
                    'IpRanges': [{
                        'CidrIp': '0.0.0.0/0',
                        'Description': 'Debug SSH access',
                    }],
                    'Ipv6Ranges': [{
                        'CidrIpv6': '::/0',
                        'Description': 'Debug SSH access',
                    }],
                },
                {
                    'IpProtocol': 'tcp',
                    'FromPort': self.settings.base_port,
                    'ToPort': self.settings.base_port + 7_000,
                    'IpRanges': [{
                        'CidrIp': '0.0.0.0/0',
                        'Description': 'Dag port',
                    }],
                    'Ipv6Ranges': [{
                        'CidrIpv6': '::/0',
                        'Description': 'Dag port',
                    }],
                },
                {
                    'IpProtocol': 'icmp',
                    'FromPort': -1,
                    'ToPort': -1,
                    'IpRanges': [{
                        'CidrIp': '0.0.0.0/0',
                        'Description': 'ping port',
                    }],
                    'Ipv6Ranges': [{
                        'CidrIpv6': '::/0',
                        'Description': 'ping port',
                    }],
                }
            ]
        )

    def _get_ami(self, client):
        # The AMI changes with regions.
        response = client.describe_images(
            Filters=[{
                'Name': 'description',
                'Values': ['Canonical, Ubuntu, 22.04 LTS, amd64 jammy image build on 2024-04-11']
            }]
        )
        return response['Images'][0]['ImageId']

    def create_instances(self, instances):
        assert isinstance(instances, int) and instances > 0

        # Create the security group in every region.
        for client in self.clients.values():
            try:
                self._create_security_group(client)
            except ClientError as e:
                error = AWSError(e)
                if error.code != 'InvalidGroup.Duplicate':
                    raise BenchError('Failed to create security group', error)

        try:
            # Create all instances.
            size = instances * len(self.clients)
            progress = progress_bar(
                self.clients.values(), prefix=f'Creating {size} instances'
            )
            for client in progress:
                client.run_instances(
                    ImageId=self._get_ami(client),
                    InstanceType=self.settings.instance_type,
                    KeyName=self.settings.key_name,
                    MaxCount=instances,
                    MinCount=instances,
                    SecurityGroups=[self.SECURITY_GROUP_NAME],
                    TagSpecifications=[{
                        'ResourceType': 'instance',
                        'Tags': [{
                            'Key': 'Name',
                            'Value': self.INSTANCE_NAME
                        }]
                    }],
                    EbsOptimized=True,
                    BlockDeviceMappings=[{
                        'DeviceName': '/dev/sda1',
                        'Ebs': {
                            'VolumeType': 'gp2',
                            'VolumeSize': 200,
                            'DeleteOnTermination': True
                        }
                    }],
                )

            # Wait for the instances to boot.
            Print.info('Waiting for all instances to boot...')
            self._wait(['pending'])
            Print.heading(f'Successfully created {size} new instances')
        except ClientError as e:
            raise BenchError('Failed to create AWS instances', AWSError(e))

    def terminate_instances(self):
        try:
            ids, _ = self._get(['pending', 'running', 'stopping', 'stopped'])
            size = sum(len(x) for x in ids.values())
            if size == 0:
                Print.heading(f'All instances are shut down')
                return

            # Terminate instances.
            for region, client in self.clients.items():
                if ids[region]:
                    client.terminate_instances(InstanceIds=ids[region])

            # Wait for all instances to properly shut down.
            Print.info('Waiting for all instances to shut down...')
            self._wait(['shutting-down'])
            for client in self.clients.values():
                client.delete_security_group(
                    GroupName=self.SECURITY_GROUP_NAME
                )

            Print.heading(f'Testbed of {size} instances destroyed')
        except ClientError as e:
            raise BenchError('Failed to terminate instances', AWSError(e))

    def start_instances(self, max):
        size = 0
        try:
            ids, _ = self._get(['stopping', 'stopped'])
            for region, client in self.clients.items():
                if ids[region]:
                    target = ids[region]
                    target = target if len(target) < max else target[:max]
                    size += len(target)
                    client.start_instances(InstanceIds=target)
            Print.heading(f'Starting {size} instances')
        except ClientError as e:
            raise BenchError('Failed to start instances', AWSError(e))

    def stop_instances(self):
        try:
            ids, _ = self._get(['pending', 'running'])
            for region, client in self.clients.items():
                if ids[region]:
                    client.stop_instances(InstanceIds=ids[region])
            size = sum(len(x) for x in ids.values())
            Print.heading(f'Stopping {size} instances')
        except ClientError as e:
            raise BenchError(AWSError(e))

    def hosts(self, flat=False):
        try:
            _, ips = self._get(['pending', 'running'])
            return [x for y in ips.values() for x in y] if flat else ips
        except ClientError as e:
            raise BenchError('Failed to gather instances IPs', AWSError(e))

    def print_info(self):
        hosts = self.hosts()
        key = self.settings.key_path
        text = ''
        for region, ips in hosts.items():
            text += f'\n Region: {region.upper()}\n'
            for i, ip in enumerate(ips):
                new_line = '\n' if (i+1) % 6 == 0 else ''
                text += f'{new_line} {i}\tssh -i {key} ubuntu@{ip}\n'
        print(
            '\n'
            '----------------------------------------------------------------\n'
            ' INFO:\n'
            '----------------------------------------------------------------\n'
            f' Available machines: {sum(len(x) for x in hosts.values())}\n'
            f'{text}'
            '----------------------------------------------------------------\n'
        )
