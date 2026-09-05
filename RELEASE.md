The pairing QR preview now uses a compact image with whole pixels per module. Large QR codes no longer fill or wrap the terminal; `--terminal-qr` displays the full terminal code when the window has enough space. The pairing protocol remains compatible with the existing Kagero App.

This version also verifies the tag-based release pipeline. The initial `v0.1.1` source tag is retained without release assets; `v0.1.2` supersedes that publishing attempt.

```sh
brew install dannisun0412/tap/kagero-host
# Existing installation:
brew upgrade dannisun0412/tap/kagero-host
kagero-host setup
```

Open Kagero → Add server → Scan to pair. After upgrading, `setup` refreshes the background executable and retains the computer identity and paired devices. Requires macOS 13+.

This is a testing release, powered by Tailcat and independent of Tailscale. Binaries are not Developer ID signed or notarized. macOS may ask for Keychain access after an unsigned executable is updated. Dependency licenses are included in both archives and `kagero-host licenses`.
