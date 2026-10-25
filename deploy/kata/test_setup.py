import io
from contextlib import redirect_stdout
import tarfile
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import setup


class SetupTests(unittest.TestCase):
    def test_completion_script_uses_cli_and_installer_tracks_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            cli = Path(tmp) / 'oaiso'
            cli.write_text('#!/bin/sh\nprintf "completion:%s\\n" "$2"\n')
            cli.chmod(0o755)
            self.assertEqual(setup.completion_script(cli, 'bash'), b'completion:bash\n')
        self.assertTrue(set(setup.COMPLETIONS.values()).issubset(set(setup.MANAGED)))

    def test_oaiso_upgrade_preserves_unmanaged_commands_and_completions(self):
        with tempfile.TemporaryDirectory() as tmp:
            for name in ['oaiso', '_oaiso', 'oaiso.fish']:
                path = Path(tmp) / name
                path.write_text('another program')
                with self.assertRaisesRegex(ValueError, 'unmanaged oaiso installation file'):
                    setup.check_oaiso_collision({'cxd_cli': True}, [path])
                self.assertEqual(path.read_text(), 'another program')
                setup.check_oaiso_collision({'oaiso_cli': True}, [path])
                setup.check_oaiso_collision(None, [path])
            path = Path(tmp) / 'absent'
            setup.check_oaiso_collision({'cxd_cli': True}, [path])
            path.symlink_to('missing-target')
            with self.assertRaisesRegex(ValueError, 'unmanaged oaiso installation file'):
                setup.check_oaiso_collision({'cxd_cli': True}, [path])

    def test_only_canonical_completion_is_installed(self):
        with patch('setup.run') as run:
            run.return_value.stdout = 'script'
            self.assertEqual(setup.completion_script('/release/bin/oaiso', 'zsh'), b'script')
            run.assert_called_once_with(['oaiso', 'completion', 'zsh'], executable='/release/bin/oaiso', capture_output=True)
        self.assertTrue(set(setup.COMPLETIONS.values()).issubset(set(setup.ACTIVE_MANAGED)))
        self.assertFalse(set(setup.LEGACY_FILES) & set(setup.ACTIVE_MANAGED))

    def test_legacy_cleanup_requires_receipt_and_unchanged_content(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / 'release/bin'
            binary.mkdir(parents=True)
            old_cli = binary / 'codex-dock'
            old_cli.write_bytes(b'old-cli')
            bindir = root / 'bin'
            bindir.mkdir()
            for name in ['cxd', 'codex-dock']:
                (bindir / name).write_bytes(old_cli.read_bytes())
            completion = root / 'cxd-completion'
            completion.write_bytes(b'old-completion')
            current = {'release': str(binary.parent), 'cxd_cli': True, 'oaiso_cli': True,
                       'bundle': {'files': {'codex-dock': setup.sha(old_cli)}}}
            with patch.object(setup, 'LEGACY_COMPLETIONS', {'cxd': {'bash': completion}, 'codex-dock': {}}), \
                    patch.object(setup, 'completion_script', return_value=b'old-completion'):
                self.assertEqual(set(setup.legacy_files_to_remove(current, bindir)),
                                 {bindir / 'cxd', bindir / 'codex-dock', completion})
                (bindir / 'cxd').write_bytes(b'another program')
                completion.write_bytes(b'custom-completion')
                self.assertEqual(setup.legacy_files_to_remove(current, bindir), [bindir / 'codex-dock'])
                self.assertEqual(setup.legacy_files_to_remove(None, bindir), [])
                self.assertEqual(setup.legacy_files_to_remove(current | {'legacy_cli_removed': True}, bindir), [])
                (bindir / 'codex-dock').unlink()
                (bindir / 'codex-dock').symlink_to(old_cli)
                self.assertEqual(setup.legacy_files_to_remove(current, bindir), [])

    def test_removed_legacy_commands_can_be_restored_on_rollback(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            old = root / 'cxd'
            old.write_bytes(b'old-cli')
            old.chmod(0o755)
            with patch.object(setup, 'MANAGED', [old]), patch.object(setup, 'trusted'):
                files = setup.backup_files(root / 'backup', [old])
                old.unlink()
                setup.restore_files(files)
            self.assertEqual(old.read_bytes(), b'old-cli')
            self.assertEqual(old.stat().st_mode & 0o777, 0o755)

    def test_readiness_supports_canonical_and_previous_release_on_rollback(self):
        checks = [{'name': name, 'status': 'ok'} for name in
                  ['node', 'builder', 'host', 'kvm', 'cgroups', 'forwarding', 'runtime']]
        for name in ['oaiso', 'codex-dock']:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                binary = Path(tmp) / 'bin'
                binary.mkdir()
                (binary / 'codex-dock').write_bytes(b'old-cli')
                (binary / name).write_bytes(b'cli')
                result = setup.subprocess.CompletedProcess([], 0, json.dumps({'checks': checks}))
                with patch.object(setup, 'run'), patch.object(setup, 'active', return_value=True), \
                        patch.object(setup.subprocess, 'run', return_value=result) as command:
                    setup.start_and_check(tmp)
                self.assertEqual(command.call_args.args[0],
                                 [str(binary / name), 'runtime-check', '--json'])

    def test_plan_shows_bundle_version_and_operator_before_apply(self):
        with tempfile.TemporaryDirectory() as tmp:
            bundle = Path(tmp)
            record = {'version': 'test-version'}
            output = io.StringIO()
            with patch('sys.argv', ['setup.py', '--bundle', str(bundle), '--operator', 'alice']), \
                    patch.object(setup, 'bundle_info', return_value=record), \
                    patch.object(setup, 'host_check'), \
                    redirect_stdout(output):
                setup.main()
            self.assertIn(f'Bundle: {bundle.resolve()} (version test-version); operator: alice', output.getvalue())
            self.assertIn('No host changes.', output.getvalue())

    def test_bundle_rejects_changed_helpers_and_extra_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {}
            for name in setup.BUNDLE_FILES:
                (root / name).write_bytes(b'trusted-fixture')
                files[name] = setup.sha(root / name)
            manifest = {'schema': 1, 'files': files}
            (root / 'bundle.json').write_text(json.dumps(manifest))
            setup.bundle_info(root)
            (root / setup.BUNDLE_FILES[1]).write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                setup.bundle_info(root)
            manifest['files']['../outside'] = 'a' * 64
            (root / 'bundle.json').write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, 'incomplete'):
                setup.bundle_info(root)

    def test_rollback_restores_contents_modes_and_absent_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            existing, absent = root / 'unit', root / 'cli'
            existing.write_bytes(b'previous service')
            existing.chmod(0o640)
            project = root / 'project'
            project.mkdir()
            (project / 'work').write_text('keep me')
            with patch.object(setup, 'MANAGED', [existing, absent]), patch.object(setup, 'trusted'):
                files = setup.backup_files(root / 'backup', [existing, absent])
                setup.atomic(existing, b'new service')
                setup.atomic(absent, b'new CLI', 0o755)
                setup.restore_files(files)
            self.assertEqual(existing.read_bytes(), b'previous service')
            self.assertEqual(existing.stat().st_mode & 0o777, 0o640)
            self.assertFalse(absent.exists())
            self.assertEqual((project / 'work').read_text(), 'keep me')

    def test_offline_rejects_missing_or_changed_archives(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaisesRegex(ValueError, 'offline archive'):
                setup.fetch('kata', root, True)
            (root / setup.LOCK['kata']['file']).write_bytes(b'corrupt')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                setup.fetch('kata', root, True)

    def test_units_only_target_dedicated_services(self):
        units = setup.units('/opt/codex-dock/releases/fixture')
        self.assertEqual(set(units), set(setup.MANAGED[:3]))
        self.assertIn('--manifest /opt/codex-dock/releases/fixture/kata-assets.json', ''.join(units.values()))
        self.assertNotIn('docker.service', ''.join(units.values()))
        self.assertIn('KillMode=process', ''.join(units.values()))
        self.assertIn('/bin/oaiso node serve', ''.join(units.values()))

    def test_archive_does_not_apply_paths_or_executable_modes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            archive = root / 'payload.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                for name in ['../escape', '/absolute-escape', 'bin/tool']:
                    item = tarfile.TarInfo(name)
                    item.size = 3
                    item.mode = 0o4777
                    tar.addfile(item, io.BytesIO(b'abc'))
            destination = root / 'selected'
            setup.extract_selected(archive, {'bin/tool': (destination, 0o755)})
            self.assertEqual(destination.read_bytes(), b'abc')
            self.assertEqual(destination.stat().st_mode & 0o7777, 0o755)
            self.assertEqual({p.name for p in root.iterdir()}, {'selected', 'payload.tar.gz'})

    def test_unavailable_engine_is_not_empty(self):
        with patch.object(setup, 'SOCKET', '/nonexistent-codex-dock-test.sock'):
            with self.assertRaisesRegex(ValueError, 'unavailable'):
                setup.empty_node({'release': '/opt/test'})

    def test_existing_vhost_device_needs_no_module_load(self):
        with patch.object(Path, 'is_char_device', return_value=True), patch.object(setup, 'run') as command:
            setup.ensure_module_device('vhost_vsock', '/dev/vhost-vsock')
            command.assert_not_called()





class ReleaseFloorTests(unittest.TestCase):
    def test_semver_order_including_numeric_prerelease(self):
        versions = ['v1.0.0-alpha', 'v1.0.0-alpha.1', 'v1.0.0-alpha.beta',
                    'v1.0.0-beta', 'v1.0.0-beta.2', 'v1.0.0-beta.11', 'v1.0.0-rc.1',
                    'v1.0.0', 'v1.0.1', 'v2.0.0']
        for a, b in zip(versions, versions[1:]):
            self.assertTrue(setup.version_less(a, b), (a, b))
            self.assertFalse(setup.version_less(b, a), (b, a))
        self.assertFalse(setup.version_less('v1.0.0+build1', 'v1.0.0+build2'))
        self.assertIsNone(setup.release_version('v1.0.0-beta.01'))

    def test_floor_survives_explicit_older_version_and_corruption_denies(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(setup, 'STATE', Path(tmp)), patch.object(setup, 'trusted'):
            path = Path(tmp) / 'release-floor.json'
            path.write_text(json.dumps({'schema': 1, 'version': 'v2.0.0'}))
            current = {'bundle': {'version': 'v1.0.0'}}
            with self.assertRaisesRegex(ValueError, 'downgrade'):
                setup.check_release_floor({'version': 'v1.5.0'}, current)
            self.assertEqual(setup.check_release_floor({'version': 'v1.5.0'}, current, True), 'v2.0.0')
            self.assertEqual(json.loads(path.read_text())['version'], 'v2.0.0')
            self.assertEqual(setup.check_release_floor({'version': 'v2.1.0'}, current), 'v2.0.0')
            path.write_text('{')
            with self.assertRaises(ValueError):
                setup.check_release_floor({'version': 'v2.1.0'}, current)


if __name__ == '__main__':
    unittest.main()
