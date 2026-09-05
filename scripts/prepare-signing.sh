#!/bin/bash
set -euo pipefail
umask 077
: "${RUNNER_TEMP:?}" "${GITHUB_ENV:?}"
: "${HOST_P12_BASE64:?Configure HOST_P12_BASE64}"
: "${HOST_P12_PASSWORD:?Configure HOST_P12_PASSWORD}"
: "${HOST_NOTARY_KEY_BASE64:?Configure HOST_NOTARY_KEY_BASE64}"
: "${HOST_NOTARY_KEY_ID:?Configure HOST_NOTARY_KEY_ID}"
: "${HOST_NOTARY_ISSUER_ID:?Configure HOST_NOTARY_ISSUER_ID}"
: "${HOST_SIGNING_IDENTITY:?Configure HOST_SIGNING_IDENTITY}"
# Never enable shell tracing: these variables contain signing credentials.
keychain="$RUNNER_TEMP/kagero-signing.keychain-db"
p12="$RUNNER_TEMP/kagero-signing.p12"
notary_key="$RUNNER_TEMP/kagero-notary.p8"
trap 'rm -f "$p12" "$notary_key"' EXIT
printf '%s' "$HOST_P12_BASE64" | base64 --decode > "$p12"
printf '%s' "$HOST_NOTARY_KEY_BASE64" | base64 --decode > "$notary_key"
keychain_password="$(openssl rand -hex 32)"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 10800 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
# codesign also uses the user search list to resolve private keys, even with
# --keychain. Preserve the runner's existing keychains while adding this one.
python3 - "$keychain" <<'PYCODE'
import shlex, subprocess, sys
existing = shlex.split(subprocess.check_output(['security', 'list-keychains', '-d', 'user'], text=True))
subprocess.run(['security', 'list-keychains', '-d', 'user', '-s', sys.argv[1],
                *[path for path in existing if path != sys.argv[1]]], check=True)
PYCODE
security import "$p12" -k "$keychain" -P "$HOST_P12_PASSWORD" -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$keychain_password" "$keychain" >/dev/null
xcrun notarytool store-credentials kagero-host-notary --key "$notary_key" \
  --key-id "$HOST_NOTARY_KEY_ID" --issuer "$HOST_NOTARY_ISSUER_ID" --keychain "$keychain"
printf 'HOST_SIGNING_KEYCHAIN=%s\nHOST_NOTARY_PROFILE=kagero-host-notary\nHOST_SIGNING_IDENTITY=%s\n' \
  "$keychain" "$HOST_SIGNING_IDENTITY" >> "$GITHUB_ENV"
