#!/usr/bin/env python3
"""Exercise release gating without using real signing keys or Apple uploads."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('sign_release', Path(__file__).with_name('sign-release.py'))
signer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(signer)


class ReleaseGateTests(unittest.TestCase):
    def invoke(self, status='Accepted', details='Authority=Developer ID Application: Example\nflags=runtime'):
        with patch.dict(os.environ, {'HOST_SIGNING_IDENTITY': 'identity', 'HOST_NOTARY_PROFILE': 'profile'}, clear=True), \
             patch.object(sys, 'argv', ['sign-release.py', '/tmp/host']), \
             patch.object(signer.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, stderr=details)) as run, \
             patch.object(signer.subprocess, 'check_output', return_value='{"status":"' + status + '","id":"sample"}') as submit:
            signer.main()
            self.assertIn('--timestamp', run.call_args_list[0].args[0])
            self.assertIn('--wait', submit.call_args.args[0])

    def test_accepted(self):
        self.invoke()

    def test_rejected(self):
        with self.assertRaises(RuntimeError):
            self.invoke('Invalid')

    def test_pending_is_not_success(self):
        with self.assertRaises(RuntimeError):
            self.invoke('In Progress')

    def test_adhoc_signature_rejected(self):
        with self.assertRaises(RuntimeError):
            self.invoke(details='Signature=adhoc\nflags=runtime')

    def test_missing_credentials(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(sys, 'argv', ['sign-release.py', '/tmp/host']), \
             patch.object(signer.subprocess, 'run') as run, self.assertRaises(SystemExit):
            signer.main()
        run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
