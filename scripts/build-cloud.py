#!/usr/bin/env python3
"""Build the native iCloud companion; signed distribution requires a real profile."""
import argparse, os, plistlib, shutil, subprocess, tempfile, json, datetime
from pathlib import Path

def validate_profile(profile, desired):
  expires = profile.get('ExpirationDate', datetime.datetime.min)
  if expires.tzinfo is None: expires = expires.replace(tzinfo=datetime.timezone.utc)
  if expires <= datetime.datetime.now(datetime.timezone.utc):
    raise ValueError('CloudKit provisioning profile has expired')
  allowed = profile.get('Entitlements', {})
  for key, value in desired.items():
    grant = allowed.get(key)
    if isinstance(value, list):
      # Apple issues icloud-services as the literal wildcard, not always an array.
      if key == 'com.apple.developer.icloud-services' and grant == '*': continue
      if not isinstance(grant, list) or not set(value).issubset(grant):
        raise ValueError('Profile missing ' + key)
    elif grant != value:
      raise ValueError('Profile mismatch: ' + key)


def main():
  root = Path(__file__).resolve().parents[1]
  parser = argparse.ArgumentParser()
  parser.add_argument('--arch', choices=['arm64','amd64'], default='arm64')
  mode = parser.add_mutually_exclusive_group()
  mode.add_argument('--signed', action='store_true', help='Sign and require notarization for distribution')
  mode.add_argument('--sign-local', action='store_true', help='Developer ID sign for local verification only; no distribution')
  args = parser.parse_args()
  arch = 'x86_64' if args.arch == 'amd64' else 'arm64'
  container = 'iCloud.com.kageroai.terminalai'
  identifier = 'app.kagero.host.cloud'
  team = '98GADGPNB3'
  entitlements = {'com.apple.developer.icloud-container-identifiers':[container],
    'com.apple.developer.icloud-services':['CloudKit'],
    'com.apple.developer.icloud-container-environment':'Production',
    'com.apple.developer.aps-environment':'production',
    'com.apple.application-identifier':team+'.'+identifier,
    'com.apple.developer.team-identifier':team}
  subprocess.run(['swift','build','--package-path',str(root/'apple'),'-c','release','--arch',arch],check=True)
  bin_dir = Path(subprocess.check_output(['swift','build','--package-path',str(root/'apple'),'-c','release','--arch',arch,'--show-bin-path'],text=True).strip())
  app = root/'dist'/('cloud-'+args.arch)/'KageroCloud.app'
  if app.exists(): shutil.rmtree(app)
  (app/'Contents/MacOS').mkdir(parents=True)
  shutil.copy2(bin_dir/'KageroCloud',app/'Contents/MacOS/KageroCloud')
  version = json.loads((root/'packaging/npm/package.json').read_text())['version']
  info = {'CFBundleIdentifier':identifier,'CFBundleName':'Kagero iCloud', 'CFBundleDisplayName':'Kagero iCloud',
    'CFBundleExecutable':'KageroCloud','CFBundlePackageType':'APPL','CFBundleShortVersionString':version,
    'CFBundleVersion':version,'LSMinimumSystemVersion':'13.0','LSUIElement':True,
    'NSPrincipalClass':'NSApplication'}
  (app/'Contents/Info.plist').write_bytes(plistlib.dumps(info))
  if not (args.signed or args.sign_local):
    subprocess.run(['codesign','--force','--sign','-',str(app)],check=True)
    print('Compile-only bundle (iCloud unavailable until provisioned):',app)
  else:
    identity = os.environ.get('HOST_SIGNING_IDENTITY','')
    profile_path = os.environ.get('HOST_CLOUD_PROFILE','')
    notary = os.environ.get('HOST_NOTARY_PROFILE','')
    if not identity or not profile_path or (args.signed and not notary):
      raise SystemExit('Require HOST_SIGNING_IDENTITY, HOST_CLOUD_PROFILE; distribution also requires HOST_NOTARY_PROFILE')
    profile = plistlib.loads(subprocess.check_output(['security','cms','-D','-i',profile_path]))
    validate_profile(profile, entitlements)
    shutil.copy2(profile_path,app/'Contents/embedded.provisionprofile')
    with tempfile.TemporaryDirectory(prefix='kagero-cloud-sign-') as temp:
      temp=Path(temp); ent=temp/'cloud.entitlements'; ent.write_bytes(plistlib.dumps(entitlements))
      signing=['codesign','--force','--sign',identity,'--options','runtime','--timestamp','--entitlements',str(ent)]
      auth=['--keychain-profile',notary]
      if os.environ.get('HOST_SIGNING_KEYCHAIN'):
        signing += ['--keychain',os.environ['HOST_SIGNING_KEYCHAIN']]
        auth += ['--keychain',os.environ['HOST_SIGNING_KEYCHAIN']]
      subprocess.run(signing+[str(app)],check=True)
      subprocess.run(['codesign','--verify','--strict',str(app)],check=True)
      details = subprocess.run(['codesign','-dvv',str(app)],capture_output=True,text=True,check=True).stderr
      if 'Authority=Developer ID Application:' not in details or 'TeamIdentifier='+team not in details or 'runtime' not in details:
        raise SystemExit('Require matching Developer ID Application and hardened runtime')
      # The optional argument must be attached; a separate prefix is parsed as a code path.
      subprocess.run(['codesign','-d','--extract-certificates='+str(temp/'cert'),str(app)],check=True)
      if (temp/'cert0').read_bytes() not in profile.get('DeveloperCertificates',[]):
        raise SystemExit('Signing certificate is not included in the CloudKit profile')
      if args.sign_local:
        print('Developer ID signed for LOCAL verification only; NOT notarized:',app)
        return
      archive=temp/'cloud.zip'
      subprocess.run(['ditto','-c','-k','--keepParent',str(app),str(archive)],check=True)
      result=json.loads(subprocess.check_output(['xcrun','notarytool','submit',str(archive),*auth,'--wait','--timeout','60m','--output-format','json'],text=True))
      if result.get('status')!='Accepted': raise SystemExit('iCloud companion notarization not accepted')
      subprocess.run(['xcrun','stapler','staple',str(app)],check=True)
    print('Signed and notarized iCloud companion:',app)

if __name__ == '__main__':
  main()
