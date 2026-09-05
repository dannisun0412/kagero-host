#!/usr/bin/env python3
"""Sign a Host executable and require Apple notarization before packaging."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


def run(*args):
    subprocess.run(args, check=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    args = parser.parse_args()
    identity = os.environ.get('HOST_SIGNING_IDENTITY', '')
    profile = os.environ.get('HOST_NOTARY_PROFILE', '')
    keychain = os.environ.get('HOST_SIGNING_KEYCHAIN', '')
    if not identity or not profile:
        parser.error('HOST_SIGNING_IDENTITY and HOST_NOTARY_PROFILE are required')
    signing = ['codesign', '--force', '--sign', identity, '--identifier',
               'app.kagero.host', '--options', 'runtime', '--timestamp']
    if keychain:
        signing += ['--keychain', keychain]
    run(*signing, str(args.binary))
    run('codesign', '--verify', '--strict', '--verbose=2', str(args.binary))
    details = subprocess.run(['codesign', '-dvv', str(args.binary)],
                             capture_output=True, text=True, check=True).stderr
    if 'Authority=Developer ID Application:' not in details or 'runtime' not in details:
        raise RuntimeError('Release requires Developer ID Application and hardened runtime')
    with tempfile.TemporaryDirectory(prefix='host-notary-') as directory:
        archive = str(Path(directory) / 'host.zip')
        run('ditto', '-c', '-k', '--keepParent', str(args.binary), archive)
        authentication = ['--keychain-profile', profile]
        if keychain:
            authentication += ['--keychain', keychain]
        reply = subprocess.check_output([
            'xcrun', 'notarytool', 'submit', archive, *authentication,
            '--wait', '--timeout', '60m', '--output-format', 'json'], text=True)
        result = json.loads(reply)
        if result.get('status') != 'Accepted':
            raise RuntimeError(f"Apple notarization not accepted: {result.get('id')} {result.get('status')}")
        print('Apple notarization accepted:', result['id'])
    # Standalone Mach-O binaries cannot carry a stapled ticket; Apple retains
    # the ticket keyed by the signature. Ship these exact bytes in brew/npm.


if __name__ == '__main__':
    main()
