#!/usr/bin/env python3
"""Run setup/update/recovery acceptance in a disposable private Ubuntu/KVM node.

Requires Docker ONLY as this test's outer disposable host. The installed product
inside it uses native containerd/Kata/Dragonball and contains no Docker daemon.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import urllib.parse
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--next-bundle', type=Path, required=True)
    parser.add_argument('--images', type=Path, required=True)
    parser.add_argument('--acceptance', type=Path, required=True)
    parser.add_argument('--acceptance-run', default='^TestKata(Acceptance|AuthenticationSession|EncryptedProfile|KeyringProfile|WorkspaceSharing|PolicyFeatures|PrimaryTerminal|GoAPI)$',
                        help='Go test name pattern for the real-Kata acceptance binary')
    parser.add_argument('--cache', type=Path, default=ROOT / '.kata-verification/setup-e2e/cache')
    parser.add_argument('--proxy-image', default='codex-dock-proxy:policy-verification')
    parser.add_argument('--network-proxy-image', default='',
                        help='Test-only proxy with synthetic upstreams and connection observers for TestKataNetworkRevocation')
    parser.add_argument('--worker-image', default='codex-dock:kata-guard',
                        help='Preloaded worker image matching the current entrypoint and pinned agents')
    parser.add_argument('--setup-python', type=Path,
                        help='Standalone Python executable; mount its installation read-only for setup verification')
    parser.add_argument('--release-installer', type=Path,
                        help='Exercise the standalone installer through a pipe for initial plan/apply')
    parser.add_argument('--uv', type=Path, help='uv executable for the standalone installer test')
    parser.add_argument('--image', default='codex-dock-setup-node:verification')
    parser.add_argument('--sudo-docker', action='store_true')
    parser.add_argument('--offline', action='store_true')
    parser.add_argument('--online-onboarding', action='store_true', help='Use a bridged disposable node to build the uncached default worker and verify fresh keyring login/init for both agents')
    args = parser.parse_args()
    if args.release_installer and not (args.setup_python and args.uv):
        parser.error('--release-installer also requires --setup-python and --uv')
    if args.setup_python:
        args.setup_python = args.setup_python.resolve(strict=True)
        if args.setup_python.parent.name != 'bin' or not os.access(args.setup_python, os.X_OK):
            parser.error('--setup-python must be an executable in a standalone installation’s bin directory')
        prefix = args.setup_python.parent.parent
        if prefix.parent == Path('/') or prefix == Path('/usr/local'):
            parser.error('--setup-python needs a dedicated standalone directory; system Python cannot be mounted')
    if os.environ.get('CODEX_DOCK_DISPOSABLE_SETUP') != '1':
        raise SystemExit('set CODEX_DOCK_DISPOSABLE_SETUP=1 on a dedicated Linux/KVM test host')
    if not Path('/dev/kvm').exists():
        raise SystemExit('/dev/kvm is required; this test never skips missing prerequisites')
    env = dict(os.environ)
    for key in ['DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH']:
        env.pop(key, None)
    docker = (['sudo', '-n'] if args.sudo_docker else []) + ['docker', '--host=unix:///var/run/docker.sock']
    def call(argv, **kwargs):
        return subprocess.run(docker + [str(x) for x in argv], env=env, check=True, **kwargs)
    call(['info'], stdout=subprocess.DEVNULL)
    assets = json.loads((ROOT / 'deploy/kata/assets.lock.json').read_text())
    args.cache.mkdir(parents=True, exist_ok=True)
    for name, asset in assets.items():
        path = args.cache / asset['file']
        if not path.is_file():
            if args.offline:
                raise SystemExit('missing offline asset: ' + str(path))
            subprocess.run(['curl', '--fail', '--location', '--proto', '=https', '--proto-redir', '=https', '--retry', '2', '--output', str(path), asset['url']], check=True)
        with path.open('rb') as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != asset['sha256']:
                raise SystemExit('asset checksum mismatch: ' + name)
    identity = uuid.uuid4().hex[:12]
    name = 'codex-dock-setup-' + identity
    volume = name + '-data'
    evidence = ROOT / '.kata-verification/setup-e2e' / identity
    evidence.mkdir(parents=True)
    # Freeze scripts before starting the node. A read-only bind of the working
    # tree still sees host edits, which can corrupt a running shell controller.
    verification = evidence / 'verification-source'
    shutil.copytree(ROOT / 'deploy/kata', verification,
                    ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
    started = False
    volume_created = False
    try:
        call(['volume', 'create', '--label', 'codex-dock.setup-test=' + identity, volume], stdout=subprocess.DEVNULL)
        volume_created = True
        argv = ['run', '-d', '--name', name, '--label', 'codex-dock.setup-test=' + identity,
                '--privileged', '--cgroupns=private', '--network=bridge' if args.online_onboarding else '--network=none',
                '--device', '/dev/kvm', '--device', '/dev/vhost-vsock', '--device', '/dev/vhost-net',
                '-e', 'CODEX_DOCK_DISPOSABLE_NODE=1', '-e', 'CODEX_DOCK_KATA_PROXY_IMAGE='+args.proxy_image,
                '-e', 'CODEX_DOCK_KATA_NETWORK_PROXY_IMAGE='+args.network_proxy_image,
                '-e', 'CODEX_DOCK_KATA_TEST_IMAGE='+args.worker_image,
                '-e', 'CODEX_DOCK_KATA_ACCEPTANCE_RUN='+args.acceptance_run,
                '--mount', f'type=volume,src={volume},dst=/var/lib/codex-dock-containerd']
        if args.online_onboarding:
            argv += ['-e', 'CODEX_DOCK_VERIFY_ONBOARDING=1']
            # Resolve the existing session proxy for the nested node; never
            # print or alter its credential-bearing URL.
            hosts = set()
            for key in ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy']:
                endpoint = urllib.parse.urlsplit(os.environ.get(key, ''))
                if endpoint.hostname and endpoint.hostname not in hosts:
                    hosts.add(endpoint.hostname)
                    argv += ['--add-host', endpoint.hostname + ':' + socket.gethostbyname(endpoint.hostname)]
            ca = os.environ.get('CODEX_PROXY_CERT')
            if ca:
                argv += ['--mount', f'type=bind,src={Path(ca).resolve(strict=True)},dst=/run/onboarding-ca.crt,readonly',
                         '-e', 'CODEX_PROXY_CERT=/run/onboarding-ca.crt']
        if args.setup_python:
            prefix = args.setup_python.parent.parent
            argv += ['-e', 'CODEX_DOCK_SETUP_PYTHON='+str(args.setup_python),
                     '--mount', f'type=bind,src={prefix},dst={prefix},readonly']
        if args.release_installer:
            argv += ['-e', 'CODEX_DOCK_RELEASE_INSTALLER=/release-install.sh',
                     '-e', 'UV_PYTHON_INSTALL_DIR='+str(args.setup_python.parent.parent.parent),
                     '--mount', f'type=bind,src={args.release_installer.resolve(strict=True)},dst=/release-install.sh,readonly',
                     '--mount', f'type=bind,src={args.uv.resolve(strict=True)},dst=/usr/local/bin/uv,readonly']
        for source, target, readonly in [
            (args.bundle, '/bundle', True), (args.next_bundle, '/bundle-next', True),
            (args.images, '/images.tar', True), (args.acceptance, '/acceptance.test', True),
            (args.cache, '/cache-input', True), (verification, '/verification', True),
            (evidence, '/evidence', False)]:
            argv += ['--mount', f'type=bind,src={source.resolve()},dst={target}' + (',readonly' if readonly else '')]
        argv += [args.image, '/bin/sh', '/verification/verify-boot.sh']
        call(argv, stdout=subprocess.DEVNULL)
        started = True
        for _ in range(100):
            result = subprocess.run(docker + ['exec', name, 'test', '-S', '/run/systemd/private'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if result.returncode == 0:
                break
            time.sleep(.2)
        else:
            raise RuntimeError('disposable systemd did not start')
        with (evidence / 'controller.log').open('w') as log:
            # Full VM/API acceptance plus install/upgrade/recovery can exceed
            # forty minutes. Individual suites retain their own shorter limits.
            call(['exec', name, '/bin/bash', '/verification/verify-node.sh'], stdout=log, stderr=subprocess.STDOUT, timeout=3600)
        print('PASS: fresh setup, real Kata, upgrade and recovery. Evidence:', evidence)
    finally:
        if started:
            # Only this controller's uniquely labelled node may be removed.
            label = subprocess.check_output(docker + ['inspect', '--format', '{{index .Config.Labels "codex-dock.setup-test"}}', name], env=env, text=True).strip()
            if label != identity:
                raise RuntimeError('test node identity changed; refusing cleanup')
            with (evidence / 'journal.log').open('w') as log:
                subprocess.run(docker + ['exec', name, 'journalctl', '--no-pager', '-u', 'codex-dock-node', '-u', 'codex-dock-buildkit', '-u', 'codex-dock-containerd', '-n', '250'], env=env, stdout=log, stderr=subprocess.STDOUT)
            subprocess.run(docker + ['exec', name, 'chown', '-R', f'{os.getuid()}:{os.getgid()}', '/evidence'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            call(['stop', '--time', '45', name], stdout=subprocess.DEVNULL)
            call(['rm', name], stdout=subprocess.DEVNULL)
        if volume_created:
            label = subprocess.check_output(docker + ['volume', 'inspect', '--format', '{{index .Labels "codex-dock.setup-test"}}', volume], env=env, text=True).strip()
            if label != identity:
                raise RuntimeError('test volume identity changed; refusing cleanup')
            call(['volume', 'rm', volume], stdout=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
