#!/usr/bin/env python3
"""Prepare GitHub Release assets and a Homebrew tap. Does not upload or publish."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import struct
import tarfile


ROOT = Path(__file__).resolve().parents[1]
ARCHITECTURES = {"arm64": (0x0100000C, "arm64"), "amd64": (0x01000007, "x86_64")}


def repository_name(value):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}", value):
        raise argparse.ArgumentTypeError("Use a GitHub repository in OWNER/REPOSITORY form.")
    return value


def check_archive(path, architecture):
    if not path.is_file():
        raise ValueError(f"Missing {path.name}; build it with package.py --arch {architecture}.")
    if path.stat().st_size > 64 * 1024 * 1024:
        raise ValueError(f"Archive exceeds 64 MB: {path.name}")
    with tarfile.open(path, "r:gz") as archive:
        expected = {"kagero-host", "LICENSE", "THIRD-PARTY-NOTICES.txt"}
        found = set()
        for member in archive:
            if member.name not in expected or member.name in found or not member.isfile():
                raise ValueError(f"Unexpected archive entry: {member.name}")
            if member.size <= 0 or member.size > 64 * 1024 * 1024:
                raise ValueError(f"Invalid archive entry size: {member.name}")
            found.add(member.name)
            if member.name == "kagero-host":
                if member.mode & 0o111 == 0:
                    raise ValueError("The archived executable is missing its execute permission.")
                handle = archive.extractfile(member)
                if handle is None:
                    raise ValueError("Cannot read the archived executable.")
                with handle:
                    header = handle.read(8)
                expected_header = (0xFEEDFACF, ARCHITECTURES[architecture][0])
                if len(header) != 8 or struct.unpack("<II", header) != expected_header:
                    raise ValueError(f"The binary in {path.name} is not a {architecture} Mach-O executable.")
        if found != expected:
            raise ValueError(f"Missing executable or license in {path.name}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


def formula(repository, version, assets):
    lines = [
        "class KageroHost < Formula",
        '  desc "Pair Kagero with your Mac using a QR code, powered by Tailcat"',
        f'  homepage "https://github.com/{repository}"',
        f'  version "{version}"',
        '  license "MIT"',
        "",
        "  depends_on :macos",
        "  on_macos do",
        "    depends_on macos: :ventura",
        "  end",
    ]
    if len(assets) == 1:
        lines.append(f"  depends_on arch: :{ARCHITECTURES[assets[0][0]][1]}")
    for architecture, name, digest in assets:
        block = "on_arm" if architecture == "arm64" else "on_intel"
        lines += [
            "", f"  {block} do",
            f'    url "https://github.com/{repository}/releases/download/v{version}/{name}"',
            f'    sha256 "{digest}"', "  end",
        ]
    lines += [
        "", "  def install", '    bin.install "kagero-host"',
        '    doc.install "LICENSE", "THIRD-PARTY-NOTICES.txt"', "  end",
        "", "  def caveats", "    <<~EOS",
        "      Run kagero-host setup to start the service at login and show a pairing QR code.",
        "      After upgrading, run kagero-host setup again to update the background service.",
        "    EOS", "  end", "", "  test do",
        f'    assert_equal "{version}", shell_output("#{{bin}}/kagero-host version").strip',
        "  end", "end", "",
    ]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", type=repository_name, required=True)
    parser.add_argument("--arch", nargs="+", choices=ARCHITECTURES, default=["arm64", "amd64"])
    parser.add_argument("--tap-repository", type=repository_name,
                        help="Defaults to OWNER/homebrew-tap.")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    version = json.loads((ROOT / "packaging/npm/package.json").read_text())["version"]
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?", version):
        parser.error("Invalid package version.")
    tap = args.tap_repository or args.repository.split("/")[0] + "/homebrew-tap"
    if not tap.split("/")[1].startswith("homebrew-") or tap.split("/")[1] == "homebrew-":
        parser.error("The tap repository name must start with homebrew- and have a nonempty suffix.")
    assets = []
    try:
        for architecture in dict.fromkeys(args.arch):
            name = f"kagero-host-{version}-darwin-{architecture}.tar.gz"
            digest = check_archive(ROOT / "dist" / name, architecture)
            assets.append((architecture, name, digest))
    except (ValueError, OSError, tarfile.TarError) as error:
        parser.error(str(error))
    output = args.output or ROOT / "dist/homebrew-release" / version
    if output.exists():
        parser.error(f"Output already exists: {output}. Choose a fresh --output directory.")
    release = output / "release"
    tap_root = output / "tap"
    release.mkdir(parents=True)
    (tap_root / "Formula").mkdir(parents=True)
    for _, name, _ in assets:
        shutil.copy2(ROOT / "dist" / name, release / name)
    (release / "SHA256SUMS").write_text("".join(f"{digest}  {name}\n" for _, name, digest in assets))
    (tap_root / "Formula/kagero-host.rb").write_text(formula(args.repository, version, assets))
    shutil.copy2(ROOT / "LICENSE", tap_root / "LICENSE")
    command = f'{tap.split("/")[0]}/{tap.split("/")[1][len("homebrew-"):]}/kagero-host'
    (tap_root / "README.md").write_text(
        f"# Kagero Host Homebrew tap\n\n"
        f"Companion source and releases: https://github.com/{args.repository}\n\n"
        "Requires macOS 13 Ventura or later.\n\n"
        f"```sh\nbrew install {command}\nkagero-host setup\n```\n\n"
        "After `brew upgrade`, run `kagero-host setup` to update the background service. "
        "The computer identity and paired devices are preserved.\n\n"
        "Powered by Tailcat. Independent of Tailscale; third-party notices are included in the package.\n")
    (output / "PUBLISH.md").write_text(
        "# Homebrew release handoff\n\nThese files have not been uploaded or published.\n\n"
        f"1. Create or open https://github.com/{args.repository}.\n"
        f"2. Publish release `v{version}` with every file in `release/` attached. "
        "Use these exact files; modifying an archive invalidates the formula checksum.\n"
        f"3. Copy the contents of `tap/` to https://github.com/{tap}.\n"
        f"4. On a clean supported Mac, run `brew install {command}`, "
        f"`brew test {command}`, and `kagero-host setup`. Pair using the App.\n\n"
        "Signing and notarization must be checked separately. This script verifies archive contents, "
        "CPU architecture and checksums; it does not certify an archive for public distribution.\n")
    print(f"Prepared Homebrew release in {output}")
    print(f"After publishing: brew install {command}")


if __name__ == "__main__":
    main()
