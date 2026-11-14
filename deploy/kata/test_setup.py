import io
from contextlib import redirect_stdout
import tarfile
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import call, patch
import setup


class SetupTests(unittest.TestCase):
    def check_host(self, release, *, system='Linux', machine='x86_64',
                   systemd=True, kvm=True, controllers='cpu memory pids', uid=1000,
                   kernel='6.8.0-generic'):
        files = {'/etc/os-release': release,
                 '/sys/fs/cgroup/cgroup.controllers': controllers}
        def read_file(path):
            value = files[str(path)]
            if isinstance(value, Exception):
                raise value
            return value
        with patch.object(setup.platform, 'system', return_value=system), \
                patch.object(setup.platform, 'machine', return_value=machine), \
                patch.object(setup.platform, 'release', return_value=kernel), \
                patch.object(Path, 'read_text', autospec=True, side_effect=read_file), \
                patch.object(Path, 'is_dir', return_value=systemd), \
                patch.object(Path, 'is_char_device', return_value=kvm), \
                patch.object(setup.pwd, 'getpwnam', return_value=SimpleNamespace(pw_uid=uid)):
            return setup.host_check('alice')

    def test_wsl_preflight_checks_storage_and_root_vm_creation(self):
        for kernel in ['6.6.87.2-microsoft-standard-WSL2', '6.18.52.3-microsoft-standard-WSL2']:
            with self.subTest(kernel=kernel), patch.object(setup, 'require_quota_storage') as quota, \
                    patch.object(setup, 'probe_kvm') as probe, patch.object(setup.os, 'geteuid', return_value=0):
                self.assertEqual(self.check_host('ID=ubuntu\nVERSION_ID="24.04"', kernel=kernel), '24.04')
                quota.assert_called_once_with()
                probe.assert_called_once_with()

    def test_wsl_plan_rejects_default_storage_without_probing_vm(self):
        with patch.object(setup, 'require_quota_storage', side_effect=ValueError('dedicated XFS required')), \
                patch.object(setup, 'probe_kvm') as probe:
            with self.assertRaisesRegex(ValueError, 'dedicated XFS.*doc/guides/wsl.md'):
                self.check_host('ID=ubuntu\nVERSION_ID=24.04', kernel='microsoft-standard-WSL2')
            probe.assert_not_called()

    def test_wsl_unprivileged_plan_defers_vm_probe_to_root(self):
        output = io.StringIO()
        with patch.object(setup, 'require_quota_storage'), patch.object(setup, 'probe_kvm') as probe, \
                patch.object(setup.os, 'geteuid', return_value=1000), redirect_stdout(output):
            self.check_host('ID=ubuntu\nVERSION_ID=24.04', kernel='microsoft-standard-WSL2')
        probe.assert_not_called()
        self.assertIn('KVM VM-creation probe runs as root during --apply', output.getvalue())

    def test_wsl_failed_vm_creation_and_host_prerequisites_have_recovery_guide(self):
        with patch.object(setup, 'require_quota_storage'), \
                patch.object(setup, 'probe_kvm', side_effect=ValueError('KVM cannot create a VM')), \
                patch.object(setup.os, 'geteuid', return_value=0):
            with self.assertRaisesRegex(ValueError, 'KVM cannot create a VM.*doc/guides/wsl.md'):
                self.check_host('ID=ubuntu\nVERSION_ID=24.04', kernel='microsoft-standard-WSL2')
        for options in [{'systemd': False}, {'kvm': False}, {'controllers': 'cpu'},
                        {'controllers': FileNotFoundError('cgroup v2 not mounted')}]:
            with self.subTest(options=options), self.assertRaisesRegex(ValueError, 'doc/guides/wsl.md'):
                self.check_host('ID=ubuntu\nVERSION_ID=24.04', kernel='microsoft-standard-WSL2', **options)

    def test_non_wsl_host_keeps_existing_plan_behavior(self):
        with patch.object(setup, 'require_quota_storage') as quota, patch.object(setup, 'probe_kvm') as probe:
            self.check_host('ID=ubuntu\nVERSION_ID=24.04')
        quota.assert_not_called()
        probe.assert_not_called()

    def test_kvm_probe_releases_all_descriptors_on_success_and_failure(self):
        for results, expected_closes, error in [([12, 41], [call(41), call(23)], None),
                                                ([13], [call(23)], 'unsupported KVM API'),
                                                ([12, OSError('nested virtualization unavailable')], [call(23)], 'KVM cannot create a VM')]:
            with self.subTest(results=results), patch.object(setup.os, 'open', return_value=23), \
                    patch.object(setup.os, 'close') as close, patch.object(setup.fcntl, 'ioctl', side_effect=results):
                if error:
                    with self.assertRaisesRegex(ValueError, error):
                        setup.probe_kvm()
                else:
                    setup.probe_kvm()
                self.assertEqual(close.call_args_list, expected_closes)
        with patch.object(setup.os, 'open', side_effect=PermissionError('denied')), \
                patch.object(setup.os, 'close') as close:
            with self.assertRaisesRegex(ValueError, 'KVM cannot create a VM'):
                setup.probe_kvm()
            close.assert_not_called()

    def test_missing_wsl_module_points_to_matching_kernel(self):
        with patch.object(Path, 'is_char_device', return_value=False), patch.object(setup, 'is_wsl', return_value=True), \
                patch.object(setup, 'run', side_effect=setup.subprocess.CalledProcessError(1, ['modprobe'])):
            with self.assertRaisesRegex(ValueError, 'modules matching uname -r.*doc/guides/wsl.md'):
                setup.ensure_module_device('vhost_vsock', '/dev/vhost-vsock')

    def test_host_accepts_supported_ubuntu_versions(self):
        for version in ['24.04', '26.04']:
            for quoted in [version, f'"{version}"']:
                with self.subTest(version=quoted):
                    self.assertEqual(self.check_host(f'ID=ubuntu\nVERSION_ID={quoted}\n'), version)

    def test_host_rejects_unsupported_releases_and_derivatives(self):
        for release in ['ID=ubuntu\nVERSION_ID="22.04"', 'ID=ubuntu\nVERSION_ID="25.10"',
                        'ID=ubuntu\nVERSION_ID="24.04.1"', 'ID=ubuntu',
                        'ID=linuxmint\nID_LIKE=ubuntu\nVERSION_ID="24.04"',
                        'ID=debian\nVERSION_ID="26.04"', '']:
            with self.subTest(release=release), self.assertRaisesRegex(ValueError, 'Ubuntu 24.04 and 26.04'):
                self.check_host(release)

    def test_both_ubuntu_versions_require_existing_host_prerequisites(self):
        for version in ['24.04', '26.04']:
            for options, error in [({'system': 'Darwin'}, 'Linux x86_64'),
                                   ({'machine': 'aarch64'}, 'Linux x86_64'),
                                   ({'systemd': False}, 'running systemd'),
                                   ({'kvm': False}, 'KVM missing'),
                                   ({'controllers': 'cpu pids'}, 'cgroup v2'),
                                   ({'uid': 0}, 'non-root')]:
                with self.subTest(version=version, options=options), self.assertRaisesRegex(ValueError, error):
                    self.check_host(f'ID=ubuntu\nVERSION_ID="{version}"', **options)

    def test_quota_storage_requires_dedicated_enforcing_xfs(self):
        for mount_id, output, accepted in [
            ('22', '22 1 7:1 / /var/lib/codex-dock-containerd rw - xfs /dev/data rw,prjquota\n', True),
            ('22', '21 1 0:1 / /var/lib/codex-dock-containerd rw - tmpfs tmpfs rw\n22 21 7:1 / /var/lib/codex-dock-containerd rw - xfs /dev/data rw,prjquota\n', True),
            ('21', '21 1 0:1 / /var/lib/codex-dock-containerd rw - tmpfs tmpfs rw\n22 21 7:1 / /var/lib/codex-dock-containerd rw - xfs /dev/data rw,prjquota\n', False),
            ('22', '22 1 7:1 / /var/lib/codex-dock-containerd rw - ext4 /dev/data rw\n', False),
            ('22', '22 1 7:1 / /var/lib/codex-dock-containerd rw - xfs /dev/data rw\n', False),
            ('22', '22 1 7:1 / / rw - xfs /dev/data rw,prjquota\n', False),
        ]:
            with self.subTest(output=output):
                if accepted:
                    setup.validate_quota_mount(mount_id, output, 2, 1)
                else:
                    with self.assertRaisesRegex(ValueError, 'dedicated XFS'):
                        setup.validate_quota_mount(mount_id, output, 2, 1)

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
                    patch.object(setup, 'host_check', return_value='24.04'), \
                    redirect_stdout(output):
                setup.main()
            self.assertIn('Plan: verified Ubuntu 24.04 amd64 node;', output.getvalue())
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

    def test_missing_tun_is_loaded_and_verified(self):
        with patch.object(Path, 'is_char_device', side_effect=[False, True]), patch.object(setup, 'run') as command:
            setup.ensure_module_device('tun', '/dev/net/tun')
            command.assert_called_once_with(['modprobe', 'tun'])
        with patch.object(Path, 'is_char_device', return_value=False), patch.object(setup, 'run'), \
                patch.object(setup, 'is_wsl', return_value=True):
            with self.assertRaisesRegex(ValueError, 'tun did not provide /dev/net/tun.*WSL'):
                setup.ensure_module_device('tun', '/dev/net/tun')





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
