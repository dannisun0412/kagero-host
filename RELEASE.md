Kagero Host 0.1.9 restores Homebrew publishing for archives containing the native iCloud companion. Release validation checks both executable architectures, required bundle files and bounded archive contents before updating the tap. Distributed executables and the iCloud companion are Developer ID signed and notarized by the release workflow.

This release includes LAN IPv4/IPv6 discovery, iCloud endpoint synchronization, and public UDP endpoint diagnostics. Public UDP endpoints retain their actual mapped port and are kept separate from TCP SSH entry points. Explicit router TCP forwarding can be advertised with `kagero-host direct --port 2223 --endpoint YOUR_DDNS_HOST:2223`.

```sh
brew install dannisun0412/tap/kagero-host
# Existing installation:
brew upgrade dannisun0412/tap/kagero-host
kagero-host setup
```

Open Kagero → Add server → Scan to pair. After upgrading, `setup` refreshes the background executable and retains the computer identity, paired devices and direct-connection configuration. Requires macOS 13+.

This is a testing release, powered by Tailcat and independent of Tailscale. Dependency licenses are included in both archives and `kagero-host licenses`.
