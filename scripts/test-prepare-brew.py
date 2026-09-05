#!/usr/bin/env python3
"""Regression checks for CLI releases with an optional signed iCloud bundle."""
import importlib.util
import io
from pathlib import Path
import struct
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("prepare_brew", Path(__file__).with_name("prepare-brew.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
ARM = struct.pack("<II", 0xFEEDFACF, 0x0100000C)
INTEL = struct.pack("<II", 0xFEEDFACF, 0x01000007)


class ArchiveTests(unittest.TestCase):
    def entries(self, cloud=False):
        result = [("kagero-host", ARM), ("LICENSE", b"license"),
                  ("THIRD-PARTY-NOTICES.txt", b"notices")]
        if cloud:
            result += [("KageroCloud.app", None),
                       ("KageroCloud.app/Contents/Info.plist", b"plist"),
                       ("KageroCloud.app/Contents/MacOS/KageroCloud", ARM),
                       ("KageroCloud.app/Contents/embedded.provisionprofile", b"profile"),
                       ("KageroCloud.app/Contents/_CodeSignature/CodeResources", b"signature")]
        return result

    def check(self, entries):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "release.tar.gz"
            with tarfile.open(path, "w:gz") as archive:
                for name, contents in entries:
                    member = tarfile.TarInfo(name)
                    member.mode = 0o755
                    if contents is None:
                        member.type = tarfile.DIRTYPE
                        archive.addfile(member)
                    elif isinstance(contents, tuple):
                        member.type = tarfile.SYMTYPE
                        member.linkname = contents[0]
                        archive.addfile(member)
                    else:
                        member.size = len(contents)
                        archive.addfile(member, io.BytesIO(contents))
            return module.check_archive(path, "arm64")

    def test_cli_and_cloud_archives(self):
        for cloud in (False, True):
            with self.subTest(cloud=cloud):
                self.assertEqual(len(self.check(self.entries(cloud))), 64)

    def test_unsafe_or_duplicate_entries(self):
        for entry in [("/tmp/escape", b"x"), ("KageroCloud.app/../escape", b"x"),
                      ("KageroCloud.app//escape", b"x"),
                      ("KageroCloud.app/link", ("/tmp/escape",)),
                      ("kagero-host", ARM), ("unexpected", b"x")]:
            with self.subTest(entry=entry), self.assertRaises(ValueError):
                self.check(self.entries(True) + [entry])

    def test_incomplete_cloud_bundle(self):
        entries = self.entries(True)
        for name, _ in entries[4:]:
            with self.subTest(missing=name), self.assertRaises(ValueError):
                self.check([entry for entry in entries if entry[0] != name])

    def test_wrong_architecture(self):
        for name in ("kagero-host", "KageroCloud.app/Contents/MacOS/KageroCloud"):
            with self.subTest(binary=name), self.assertRaises(ValueError):
                self.check([(key, INTEL if key == name else value)
                            for key, value in self.entries(True)])

    def test_file_cannot_be_parent(self):
        with self.assertRaises(ValueError):
            self.check(self.entries(True) + [("KageroCloud.app/Contents", b"file")])

    def test_entry_limit(self):
        with self.assertRaises(ValueError):
            self.check(self.entries(True) + [(f"KageroCloud.app/{index}", b"x")
                                             for index in range(256)])


if __name__ == "__main__":
    unittest.main()
