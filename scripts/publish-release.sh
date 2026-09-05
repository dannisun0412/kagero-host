#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

: "${GITHUB_REPOSITORY:?Run this script in the companion release workflow}"
: "${RELEASE_TAG:?Missing release tag}"
version="$(python3 scripts/check-version.py --tag "$RELEASE_TAG")"
release_state="$RUNNER_TEMP/kagero-release.json"

# A retry after a successful release must reuse its original bytes, not overwrite it.
if gh release view "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --json isDraft > "$release_state" 2>/dev/null; then
  if python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1]))["isDraft"])' "$release_state"; then
    mkdir -p dist/published
    gh release download "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --dir dist/published \
      --pattern "kagero-host-$version-darwin-*.tar.gz" --pattern SHA256SUMS
    (cd dist/published && sha256sum --check SHA256SUMS)
    cp "dist/published/kagero-host-$version-darwin-arm64.tar.gz" dist/
    cp "dist/published/kagero-host-$version-darwin-amd64.tar.gz" dist/
  fi
fi

python3 scripts/prepare-brew.py --repository "$GITHUB_REPOSITORY" --output dist/publish
if ! test -s "$release_state"; then
  gh release create "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --verify-tag --draft --prerelease \
    --title "Kagero Host $version" --notes-file RELEASE.md dist/publish/release/*
elif python3 -c 'import json,sys; sys.exit(not json.load(open(sys.argv[1]))["isDraft"])' "$release_state"; then
  gh release upload "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --clobber dist/publish/release/*
fi
gh release edit "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false

# Only the formula is updated: other tap packages and documentation are preserved.
cp dist/publish/tap/Formula/kagero-host.rb tap/Formula/kagero-host.rb
git -C tap config user.name 'github-actions[bot]'
git -C tap config user.email '41898282+github-actions[bot]@users.noreply.github.com'
git -C tap add Formula/kagero-host.rb
if ! git -C tap diff --cached --quiet; then
  git -C tap commit -m "Update Kagero Host to $version"
  git -C tap push origin HEAD:main
fi
