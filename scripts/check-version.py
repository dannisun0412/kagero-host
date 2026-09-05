#!/usr/bin/env python3
"""Validate the manifest, executable version and optional release tag."""
import argparse
import json
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--tag')
parser.add_argument('--prefix', choices=['v', 'host-v'], default='v')
args = parser.parse_args()
version = json.loads((root / 'packaging/npm/package.json').read_text())['version']
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
    parser.error('Version must be MAJOR.MINOR.PATCH without leading zeros.')
match = re.search(r'^const Version = "([^\"]+)"$', (root / 'internal/host/state.go').read_text(), re.MULTILINE)
if match is None or match[1] != version:
    parser.error('Go Version and package.json version do not match.')
if args.tag is not None and args.tag != args.prefix + version:
    parser.error(f'Tag must match {args.prefix}{version}; existing releases must use a new version.')
print(version)
