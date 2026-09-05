#!/usr/bin/env python3
"""Build local npm + Homebrew packages, with licenses, without publishing anything."""
import argparse, hashlib, json, os, platform, re, shutil, subprocess, tarfile
from pathlib import Path

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--arch', choices=['arm64', 'amd64'], default='arm64' if platform.machine() == 'arm64' else 'amd64')
parser.add_argument('--signed', action='store_true', help='Require Developer ID signing and Apple notarization')
options = parser.parse_args()
version = json.loads((root / 'packaging/npm/package.json').read_text())['version']
if not re.fullmatch(r'\d+\.\d+\.\d+', version):
    raise RuntimeError('Use a numeric MAJOR.MINOR.PATCH version.')
if f'const Version = "{version}"' not in (root / 'internal/host/state.go').read_text():
    raise RuntimeError('Go and package.json versions must match before packaging.')
dist = root / 'dist'
dist.mkdir(exist_ok=True)
env = dict(os.environ, GOOS='darwin', GOARCH=options.arch, CGO_ENABLED='1',
           MACOSX_DEPLOYMENT_TARGET='13.0', CGO_CFLAGS='-O2 -g -mmacosx-version-min=13.0',
           CGO_LDFLAGS='-mmacosx-version-min=13.0',
           CC='clang -arch ' + ('x86_64' if options.arch == 'amd64' else 'arm64'))
tags = 'osusergo,netgo,ts_omit_ssh,ts_omit_osrouter,ts_omit_dns,ts_omit_logtail,ts_omit_netlog,ts_omit_webclient,ts_omit_taildrop,ts_omit_drive,ts_omit_aws,ts_omit_kube,ts_omit_systray'
raw = subprocess.check_output(['go', 'list', '-deps', '-json', '-tags', tags, './cmd/kagero-host'], cwd=root, env=env, text=True)
decoder = json.JSONDecoder(); modules = {}
while raw.strip():
    item, end = decoder.raw_decode(raw.lstrip()); raw = raw.lstrip()[end:]
    module = item.get('Module')
    if module and not module.get('Main'):
        modules[module['Path']] = (module['Version'], Path(module['Dir']))
goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], cwd=root, env=env, text=True).strip())
sections = ['Kagero Host uses Tailcat. An independent project, not endorsed by Tailscale.\nhttps://github.com/tailscale/tailcat', 'Go standard library\n' + (goroot/'LICENSE').read_text()]
for name, (version_id, directory) in sorted(modules.items()):
    files = sorted(p for p in directory.rglob('*') if p.is_file() and p.name.upper().split('.')[0] in ('LICENSE', 'LICENCE', 'COPYING', 'NOTICE') and p.suffix.lower() not in ('.go', '.json', '.sum'))
    if not files: raise RuntimeError('Missing license: ' + name)
    sections.append(name + ' ' + version_id + '\n' + '\n'.join(str(p.relative_to(directory)) + '\n' + p.read_text(errors='replace') for p in files))
notice = '\n\n'.join(sections)
(root/'internal/host/THIRD-PARTY-NOTICES.txt').write_text(notice)
binary = dist / ('kagero-host-darwin-' + options.arch)
subprocess.run(['go', 'build', '-trimpath', '-tags', tags, '-ldflags=-s -w', '-o', str(binary), './cmd/kagero-host'], cwd=root, env=env, check=True)
build_info = subprocess.check_output(['xcrun', 'vtool', '-show-build', str(binary)], text=True)
if not re.search(r'^\s*minos 13\.0\s*$', build_info, re.MULTILINE):
    raise RuntimeError('The built executable must target macOS 13.0; refusing to package it.')
if options.signed:
    subprocess.run(['python3', str(root/'scripts/sign-release.py'), str(binary)], check=True)
package = dist / 'npm'
package.mkdir(exist_ok=True)
for filename in ('package.json', 'cli.cjs'):
    shutil.copy(root/'packaging/npm'/filename, package/filename)
(package/'bin').mkdir(exist_ok=True)
shutil.copy2(binary, package/'bin'/binary.name)
(package/'THIRD-PARTY-NOTICES.txt').write_text(notice)
for filename in ('README.md', 'LICENSE'): shutil.copy(root/filename, package/filename)
subprocess.run(['npm', 'pack', '--pack-destination', str(dist)], cwd=package, check=True)
archive = dist / f'kagero-host-{version}-darwin-{options.arch}.tar.gz'
with tarfile.open(archive, 'w:gz') as tar:
    tar.add(binary, arcname='kagero-host')
    tar.add(root/'LICENSE', arcname='LICENSE')
    tar.add(package/'THIRD-PARTY-NOTICES.txt', arcname='THIRD-PARTY-NOTICES.txt')
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
formula = f'''class KageroHost < Formula
  desc "Pair Kagero with your Mac using a QR code, powered by Tailcat"
  homepage "https://github.com/tailscale/tailcat"
  url "{archive.as_uri()}"
  sha256 "{digest}"
  version "{version}"
  license "MIT"
  depends_on :macos
  on_macos do
    depends_on macos: :ventura
  end
  def install
    bin.install "kagero-host"
    doc.install "LICENSE", "THIRD-PARTY-NOTICES.txt"
  end
  def caveats
    "Run kagero-host setup to enable login startup and show the pairing QR code."
  end
  test do
    assert_equal "{version}", shell_output("#{{bin}}/kagero-host version").strip
  end
end
'''
(dist/'kagero-host.rb').write_text(formula)
print('Local packages ready in', dist)
