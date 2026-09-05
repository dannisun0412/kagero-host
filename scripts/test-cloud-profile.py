#!/usr/bin/env python3
"""Check Apple profile grant matching without using private signing credentials."""
import datetime
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('cloud_build', Path(__file__).with_name('build-cloud.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

SERVICES = 'com.apple.developer.icloud-services'
CONTAINERS = 'com.apple.developer.icloud-container-identifiers'
IDENTITY = 'com.apple.application-identifier'
DESIRED = {SERVICES: ['CloudKit'], CONTAINERS: ['iCloud.example'], IDENTITY: 'TEAM.app.example'}

class CloudProfileTests(unittest.TestCase):
    def profile(self, **kwargs):
        value = {'ExpirationDate': datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=1),
                 'Entitlements': dict(DESIRED)}
        value.update(kwargs)
        return value

    def test_apple_service_wildcard_and_explicit_list(self):
        p = self.profile()
        module.validate_profile(p, DESIRED)
        p['Entitlements'][SERVICES] = '*'
        module.validate_profile(p, DESIRED)

    def test_wildcard_does_not_authorize_other_containers(self):
        p = self.profile()
        p['Entitlements'][CONTAINERS] = '*'
        with self.assertRaises(ValueError): module.validate_profile(p, DESIRED)

    def test_wrong_service_and_identity_rejected(self):
        for key, value in [(SERVICES, ['CloudDocuments']), (IDENTITY, 'OTHER.app.example'), (CONTAINERS, ['iCloud.other'])]:
            with self.subTest(key=key):
                p = self.profile(); p['Entitlements'][key] = value
                with self.assertRaises(ValueError): module.validate_profile(p, DESIRED)

    def test_expired_and_missing_expiry_rejected(self):
        p = self.profile(ExpirationDate=datetime.datetime(2000, 1, 1))
        with self.assertRaises(ValueError): module.validate_profile(p, DESIRED)
        del p['ExpirationDate']
        with self.assertRaises(ValueError): module.validate_profile(p, DESIRED)

if __name__ == '__main__':
    unittest.main()
