# Copyright(C) Facebook, Inc. and its affiliates.
from json import load, JSONDecodeError


class SettingsError(Exception):
    pass


class Settings:
    def __init__(self, key_name, key_path, base_port, instance_type, aws_regions):
        inputs_str = [
            key_name, key_path, instance_type
        ]
        if isinstance(aws_regions, list):
            regions = aws_regions
        else:
            regions = [aws_regions]
        inputs_str += regions
        ok = all(isinstance(x, str) for x in inputs_str)
        ok &= isinstance(base_port, int)
        ok &= len(regions) > 0
        if not ok:
            raise SettingsError('Invalid settings types')

        self.key_name = key_name
        self.key_path = key_path

        self.base_port = base_port

        self.instance_type = instance_type
        self.aws_regions = regions

    @classmethod
    def load(cls, filename):
        try:
            with open(filename, 'r') as f:
                data = load(f)
                
            return cls(
                data['key']['name'],
                data['key']['path'],
                data['port'],
                data['instances']['type'],
                data['instances']['regions'],
            )
        except (OSError, JSONDecodeError) as e:
            raise SettingsError(str(e))

        except KeyError as e:
            raise SettingsError(f'Malformed settings: missing key {e}')
