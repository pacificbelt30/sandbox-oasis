import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import platform
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import types
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = types.ModuleType('release_installer')
PAYLOAD = (ROOT / 'install.sh').read_text().split("<<'PYINSTALLER'\n", 1)[1].split('\nPYINSTALLER', 1)[0]
exec(compile(PAYLOAD, str(ROOT / 'install.sh'), 'exec'), INSTALLER.__dict__)
SPEC = importlib.util.spec_from_file_location('package_release', ROOT / 'scripts/package-release.py')
PACKAGER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PACKAGER)


def bundle(root, version='v1.2.3'):
    root.mkdir()
    checksums = {}
    for name in INSTALLER.FILES:
        header = bytearray(120)
        header[:6] = b'\x7fELF\x02\x01'
        header[18:20] = b'\x3e\x00'
        struct.pack_into('<Q', header, 32, 64)
        struct.pack_into('<HH', header, 54, 56, 1)
        struct.pack_into('<I', header, 64, 1)
        path = root / name
        path.write_bytes(header)
        path.chmod(0o755)
        checksums[name] = hashlib.sha256(header).hexdigest()
    (root / 'bundle.json').write_text(json.dumps({'schema': 1, 'version': version, 'files': checksums}))
    for name in ['LICENSE', 'THIRD_PARTY_NOTICES.txt']:
        (root / name).write_text('fixture notices')
    return root


def archive_bundle(directory, archive):
    with tarfile.open(archive, 'w:gz') as output:
        for name in INSTALLER.FILES | INSTALLER.DATA_FILES:
            output.add(directory / name, arcname=name)


class ReleaseIntegrityTests(unittest.TestCase):
    def test_release_roundtrip_and_reproducible_packaging(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = bundle(root / 'source')
            with patch.object(PACKAGER.subprocess, 'check_output', return_value='oaiso version v1.2.3\n'), \
                    patch.object(PACKAGER, 'collect_notices', return_value='fixture notices'), \
                    patch.object(PACKAGER, 'collect_sbom', return_value={'bomFormat': 'CycloneDX', 'specVersion': '1.6', 'version': 1, 'components': []}):
                for name in ['first', 'second']:
                    PACKAGER.package(source, root / name, 'v1.2.3', ROOT / 'install.sh')
            self.assertEqual((root / 'first/oaiso-linux-amd64.tar.gz').read_bytes(),
                             (root / 'second/oaiso-linux-amd64.tar.gz').read_bytes())
            self.assertIn("DEFAULT_VERSION = 'v1.2.3'", (root / 'first/install.sh').read_text())
            destination = root / 'unpacked'
            destination.mkdir()
            INSTALLER.unpack(root / 'first/oaiso-linux-amd64.tar.gz', destination)
            INSTALLER.validate_bundle(destination, 'v1.2.3')
            for line in (root / 'first/SHA256SUMS').read_text().splitlines():
                checksum, name = line.split()
                self.assertEqual(hashlib.sha256((root / 'first' / name).read_bytes()).hexdigest(), checksum)

    def test_packager_rejects_dynamic_binary_changed_bytes_and_wrong_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = bundle(root / 'source')
            with self.assertRaisesRegex(ValueError, 'version'):
                PACKAGER.package(source, root / 'out', 'v2.0.0', ROOT / 'install.sh')
            path = source / 'oaiso'
            data = bytearray(path.read_bytes())
            data[-1] = 1
            path.write_bytes(data)
            with self.assertRaisesRegex(ValueError, 'checksum'):
                PACKAGER.package(source, root / 'out', 'v1.2.3', ROOT / 'install.sh')
            struct.pack_into('<I', data, 64, 3)
            path.write_bytes(data)
            with self.assertRaisesRegex(ValueError, 'statically linked'):
                PACKAGER.package(source, root / 'out', 'v1.2.3', ROOT / 'install.sh')

    def test_bundle_rejects_wrong_version_corruption_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = bundle(Path(temporary) / 'source')
            with self.assertRaisesRegex(ValueError, 'version'):
                INSTALLER.validate_bundle(source, 'v2.0.0')
            path = source / 'oaiso'
            original = path.read_bytes()
            path.write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                INSTALLER.validate_bundle(source)
            path.unlink()
            outside = source.parent / 'outside'
            outside.write_bytes(original)
            outside.chmod(0o755)
            path.symlink_to(outside)
            with self.assertRaisesRegex(ValueError, 'invalid bundle executable'):
                INSTALLER.validate_bundle(source)

    def test_archive_rejects_paths_links_duplicates_and_incomplete_payload(self):
        cases = [('../escape', tarfile.REGTYPE), ('/escape', tarfile.REGTYPE),
                 ('oaiso', tarfile.SYMTYPE), ('oaiso', tarfile.LNKTYPE), ('oaiso', tarfile.FIFOTYPE)]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for index, (name, kind) in enumerate(cases):
                with self.subTest(name=name, kind=kind):
                    archive = root / f'bad-{index}.tar.gz'
                    info = tarfile.TarInfo(name)
                    info.type = kind
                    info.linkname = '../outside' if kind in [tarfile.SYMTYPE, tarfile.LNKTYPE] else ''
                    with tarfile.open(archive, 'w:gz') as output:
                        output.addfile(info, io.BytesIO())
                    destination = root / f'out-{index}'
                    destination.mkdir()
                    with self.assertRaises(ValueError):
                        INSTALLER.unpack(archive, destination)
            for duplicate in [False, True]:
                archive = root / 'duplicate.tar.gz'
                with tarfile.open(archive, 'w:gz') as output:
                    output.addfile(tarfile.TarInfo('oaiso'), io.BytesIO())
                    if duplicate:
                        output.addfile(tarfile.TarInfo('oaiso'), io.BytesIO())
                with self.assertRaises(ValueError):
                    INSTALLER.unpack(archive, Path(tempfile.mkdtemp(dir=root)))
            self.assertFalse((root / 'outside').exists())

    def test_expanded_size_is_rejected_before_reading_payload(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            info = tarfile.TarInfo('oaiso')
            info.size = 513 << 20
            archive = root / 'oversized.tar.gz'
            archive.write_bytes(gzip.compress(info.tobuf() + b'\0' * 1024))
            with self.assertRaisesRegex(ValueError, 'size limit'):
                INSTALLER.unpack(archive, root)

    def test_download_integrity_and_version_checked_before_execution(self):
        for mismatch in ['checksum', 'version']:
            with self.subTest(mismatch=mismatch), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source = bundle(root / 'source')
                archive = root / 'fixture.tar.gz'
                archive_bundle(source, archive)
                digest = hashlib.sha256(archive.read_bytes()).hexdigest()
                def download(url, target, limit):
                    if url.endswith('SHA256SUMS'):
                        target.write_text(('0' * 64 if mismatch == 'checksum' else digest) + '  ' + INSTALLER.ARCHIVE + '\n')
                    else:
                        target.write_bytes(archive.read_bytes())
                destination = root / 'download'
                destination.mkdir()
                with patch.object(INSTALLER, 'download', side_effect=download), patch.object(INSTALLER.subprocess, 'run') as execute, patch.object(INSTALLER, 'verify_attestation'):
                    with self.assertRaisesRegex(ValueError, mismatch):
                        INSTALLER.get_release(destination, 'v2.0.0' if mismatch == 'version' else 'v1.2.3')
                    execute.assert_not_called()

    def test_pinned_version_does_not_consult_latest_and_redirects_require_https(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(INSTALLER, 'download', side_effect=RuntimeError('stop')) as download:
                with self.assertRaises(RuntimeError):
                    INSTALLER.get_release(Path(temporary), 'v1.2.3')
                self.assertIn('/download/v1.2.3/', download.call_args.args[0])
            with self.assertRaises(ValueError):
                INSTALLER.HTTPSRedirect().redirect_request(None, None, 302, '', {}, 'http://example.com/file')


@unittest.skipUnless(platform.system() == 'Linux' and platform.machine() == 'x86_64', 'node installer targets Linux x86_64')
class PipedInstallerTests(unittest.TestCase):
    def test_pipe_with_offline_bundle_precedes_apply_and_passes_absolute_python(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = bundle(root / 'bundle with spaces')
            cli = directory / 'oaiso'
            cli.write_text('#!/bin/sh\nif [ "$1" = --version ]; then printf "oaiso version v1.2.3\\n"; exit; fi\nprintf "%s\\n" "$*" >> "$RELEASE_TEST_LOG"\n')
            record = json.loads((directory / 'bundle.json').read_text())
            record['files']['oaiso'] = hashlib.sha256(cli.read_bytes()).hexdigest()
            (directory / 'bundle.json').write_text(json.dumps(record))
            tools = root / 'tools'
            tools.mkdir()
            for name in ['uname', 'id']:
                (tools / name).symlink_to(shutil.which(name))
            uv = tools / 'uv'
            uv.write_text('#!/bin/sh\nprintf "%s\\n" "$RELEASE_TEST_PYTHON"\n')
            uv.chmod(0o755)
            sudo = tools / 'sudo'
            sudo.write_text('#!/bin/sh\nprintf "sudo\\n" >> "$RELEASE_TEST_LOG"\nshift\nexec "$@"\n')
            sudo.chmod(0o755)
            log = root / 'calls'
            python = os.path.realpath(sys._base_executable)
            env = os.environ | {'PATH': str(tools), 'RELEASE_TEST_PYTHON': python, 'RELEASE_TEST_LOG': str(log)}
            result = subprocess.run([shutil.which('bash'), '-s', '--', '--bundle', str(directory), '--operator', 'alice', '--offline', '--apply'],
                                    input=(ROOT / 'install.sh').read_text(), env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            calls = log.read_text().splitlines()
            self.assertIn('--python ' + python, calls[0])
            self.assertNotIn('--apply', calls[0])
            self.assertTrue(calls[-1].endswith('--apply'))
            self.assertEqual(calls[0], calls[-1].removesuffix(' --apply'))

    def test_offline_without_bundle_fails_before_bootstrap(self):
        result = subprocess.run([shutil.which('bash'), '-s', '--', '--offline', '--operator', 'alice'],
                                input=(ROOT / 'install.sh').read_text(), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--offline requires --bundle', result.stderr)





class SignatureBoundaryTests(unittest.TestCase):
    def test_bad_signature_refuses_before_unpack(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = bundle(root / 'source')
            archive = root / 'fixture.tar.gz'
            archive_bundle(source, archive)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            def download(url, target, limit):
                if url.endswith('SHA256SUMS'):
                    target.write_text(digest + '  ' + INSTALLER.ARCHIVE + '\n')
                elif url.endswith('provenance.json'):
                    target.write_text('{}')
                else:
                    target.write_bytes(archive.read_bytes())
            destination = root / 'download'
            destination.mkdir()
            with patch.object(INSTALLER, 'download', side_effect=download), \
                    patch.object(INSTALLER.shutil, 'which', return_value='/fixture/gh'), \
                    patch.object(INSTALLER.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'gh')) as verify, \
                    patch.object(INSTALLER, 'unpack') as unpack:
                with self.assertRaises(subprocess.CalledProcessError):
                    INSTALLER.get_release(destination, 'v1.2.3')
                unpack.assert_not_called()
                argv = verify.call_args.args[0]
                self.assertIn('refs/tags/v1.2.3', argv)
                self.assertIn('https://github.com/' + INSTALLER.REPOSITORY + '/.github/workflows/release.yml@refs/tags/v1.2.3', argv)
                selectors = {'--cert-identity', '--cert-identity-regex', '--signer-repo', '--signer-workflow'}
                self.assertEqual(sum(argument in selectors for argument in argv), 1)
                self.assertIn('--cert-oidc-issuer', argv)
                self.assertIn('--deny-self-hosted-runners', argv)

    def test_sbom_uses_built_binary_dependencies_and_runtime_pins(self):
        info = {'GoVersion': 'go1.27.3', 'Deps': [{'Path': 'example.org/dependency', 'Version': 'v1.2.3', 'Sum': 'h1:fixture'}]}
        with tempfile.TemporaryDirectory() as temporary:
            source = bundle(Path(temporary) / 'source')
            with patch.object(PACKAGER.subprocess, 'check_output', return_value=json.dumps(info)):
                inventory = PACKAGER.collect_sbom(source, 'v1.2.3')
            self.assertEqual(inventory['bomFormat'], 'CycloneDX')
            dependencies = [item for item in inventory['components'] if item['name'] == 'example.org/dependency']
            self.assertEqual(len(dependencies), 1)
            self.assertEqual(dependencies[0]['version'], 'v1.2.3')
            self.assertTrue(any(item['name'] == 'kata' for item in inventory['components']))
            self.assertEqual(len([item for item in inventory['components'] if item['type'] == 'file']), 3)


class SourceBindingTests(unittest.TestCase):
    def test_stale_or_dirty_binary_is_not_publishable(self):
        revision = 'a' * 40
        for recorded, dirty in [('b' * 40, 'false'), (revision, 'true'), (None, 'false')]:
            info = {'Settings': [{'Key': 'vcs.revision', 'Value': recorded}, {'Key': 'vcs.modified', 'Value': dirty}]}
            with patch.object(PACKAGER.subprocess, 'check_output', side_effect=[revision, '', json.dumps(info)]):
                with self.assertRaisesRegex(ValueError, 'revision/cleanliness'):
                    PACKAGER.verify_source(Path('/fixture'))
        info = {'Settings': [{'Key': 'vcs.revision', 'Value': revision}, {'Key': 'vcs.modified', 'Value': 'false'}]}
        with patch.object(PACKAGER.subprocess, 'check_output', side_effect=[revision, '', *[json.dumps(info)] * 3]):
            PACKAGER.verify_source(Path('/fixture'))


if __name__ == '__main__':
    unittest.main()
