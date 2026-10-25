#!/usr/bin/env python3
"""Package a matching static Linux amd64 node bundle for GitHub Releases."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import tarfile
import urllib.parse


FILES = ['oaiso', 'codex-dock-worker-guard', 'codex-dock-io-logger']
ROOT = Path(__file__).resolve().parents[1]


def collect_notices():
    """Ship the Go toolchain and downloaded dependency license/notice texts."""
    goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    sections = ['Go toolchain\n' + (goroot / 'LICENSE').read_text()]
    remaining = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/oaiso', './cmd/worker-guard', './cmd/io-logger'],
                                       cwd=ROOT, text=True, env=os.environ | {'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'amd64'})
    decoder = json.JSONDecoder()
    modules = {}
    while remaining.strip():
        entry, end = decoder.raw_decode(remaining.lstrip())
        remaining = remaining.lstrip()[end:]
        module = entry.get('Module')
        if not module or module.get('Main'):
            continue
        module = module.get('Replace') or module
        if not module.get('Dir'):
            raise ValueError('missing dependency directory: ' + module['Path'])
        modules[module['Path']] = module
    for module in sorted(modules.values(), key=lambda entry: entry['Path']):
        notices = [path for path in Path(module['Dir']).iterdir() if path.is_file()
                   and path.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'COPYRIGHT', 'NOTICE'))]
        if not notices:
            raise ValueError('dependency has no license/notice file: ' + module['Path'])
        for path in sorted(notices):
            sections.append(module['Path'] + '@' + module.get('Version', '') + ' / ' + path.name + '\n' + path.read_text(errors='replace'))
    return '\n\n' + ('\n\n' + '=' * 72 + '\n\n').join(sections)


def collect_sbom(bundle, version):
    components = {}
    binaries = []
    for name in FILES:
        path = bundle / name
        info = json.loads(subprocess.check_output(['go', 'version', '-m', '-json', str(path)], text=True))
        binaries.append({'type': 'file', 'name': name, 'bom-ref': 'file:' + name,
                         'hashes': [{'alg': 'SHA-256', 'content': hashlib.sha256(path.read_bytes()).hexdigest()}]})
        toolchain = info['GoVersion']
        components['stdlib:' + toolchain] = {'type': 'library', 'name': 'Go standard library', 'version': toolchain,
                                           'bom-ref': 'stdlib:' + toolchain}
        for module in info.get('Deps', []):
            module = module.get('Replace') or module
            if not module.get('Version') or not module.get('Sum'):
                raise ValueError('release dependency lacks immutable module identity: ' + module['Path'])
            ref = 'pkg:golang/' + urllib.parse.quote(module['Path'], safe='/') + '@' + urllib.parse.quote(module['Version'], safe='')
            components[ref] = {'type': 'library', 'name': module['Path'], 'version': module['Version'],
                               'bom-ref': ref, 'purl': ref,
                               'properties': [{'name': 'go:module:sum', 'value': module['Sum']}]}
    for name, asset in json.loads((ROOT / 'deploy/kata/assets.lock.json').read_text()).items():
        components['runtime:' + name] = {'type': 'application', 'name': name, 'version': asset['version'],
                                         'bom-ref': 'runtime:' + name,
                                         'hashes': [{'alg': 'SHA-256', 'content': asset['sha256']}],
                                         'externalReferences': [{'type': 'distribution', 'url': asset['url']}]}
    return {'bomFormat': 'CycloneDX', 'specVersion': '1.6', 'version': 1,
            'metadata': {'component': {'type': 'application', 'name': 'Oaiso', 'version': version},
                         'properties': [{'name': 'oaiso:coverage', 'value': 'Host executables, linked Go modules and pinned downloaded runtime assets; excludes guest images and OS packages'}]},
            'components': sorted([*components.values(), *binaries], key=lambda item: item['bom-ref'])}


def verify_source(bundle):
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip():
        raise ValueError('release source tree must be clean')
    for name in FILES:
        info = json.loads(subprocess.check_output(['go', 'version', '-m', '-json', str(bundle / name)], text=True))
        settings = {item['Key']: item['Value'] for item in info.get('Settings', [])}
        if settings.get('vcs.revision') != revision or settings.get('vcs.modified') != 'false':
            raise ValueError('release binary source revision/cleanliness mismatch: ' + name)


def package(bundle, output, version, installer, require_clean_source=False):
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?', version):
        raise ValueError('expected a release tag such as v0.0.2')
    record = json.loads((bundle / 'bundle.json').read_text())
    if record.get('schema') != 1 or record.get('version') != version or set(record.get('files', {})) != set(FILES):
        raise ValueError('bundle manifest/version does not match the release')
    for name in FILES:
        path = bundle / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('bundle executable must be a regular file: ' + name)
        with path.open('rb') as stream:
            header = stream.read(64)
            if len(header) != 64 or header[:6] != b'\x7fELF\x02\x01' or header[18:20] != b'\x3e\x00':
                raise ValueError('release executables must be Linux amd64 ELF')
            offset = struct.unpack_from('<Q', header, 32)[0]
            width, count = struct.unpack_from('<HH', header, 54)
            if width != 56 or count > 1024 or offset + width * count > path.stat().st_size:
                raise ValueError('invalid ELF program headers')
            stream.seek(offset)
            for _ in range(count):
                if struct.unpack_from('<I', stream.read(width))[0] == 3:
                    raise ValueError('release executables must be statically linked')
            stream.seek(0)
            if hashlib.file_digest(stream, 'sha256').hexdigest() != record['files'][name]:
                raise ValueError('bundle executable checksum mismatch: ' + name)
    if require_clean_source:
        verify_source(bundle)
    if subprocess.check_output([str((bundle / 'oaiso').resolve()), '--version'], text=True).strip() != 'oaiso version ' + version:
        raise ValueError('CLI version does not match release tag')
    output.mkdir(parents=True, exist_ok=True)
    notices = collect_notices().encode()
    data_files = {'bundle.json': (bundle / 'bundle.json').read_bytes(),
                  'LICENSE': (ROOT / 'LICENSE').read_bytes(), 'THIRD_PARTY_NOTICES.txt': notices}
    archive = output / 'oaiso-linux-amd64.tar.gz'
    with archive.open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', mtime=0, filename='') as compressed:
        with tarfile.open(fileobj=compressed, mode='w', format=tarfile.USTAR_FORMAT) as destination:
            for name in FILES:
                path = bundle / name
                info = tarfile.TarInfo(name)
                info.size = path.stat().st_size
                info.mode = 0o644 if name == 'bundle.json' else 0o755
                with path.open('rb') as source:
                    destination.addfile(info, source)
            for name, data in data_files.items():
                info = tarfile.TarInfo(name)
                info.size = len(data)
                info.mode = 0o644
                destination.addfile(info, io.BytesIO(data))
    script = installer.read_text()
    marker = 'DEFAULT_VERSION = None  # release packaging sets this value'
    if script.count(marker) != 1:
        raise ValueError('installer is missing its release version marker')
    (output / 'install.sh').write_text(script.replace(marker, 'DEFAULT_VERSION = ' + repr(version)))
    (output / 'sbom.cdx.json').write_text(json.dumps(collect_sbom(bundle, version), sort_keys=True, indent=2) + '\n')
    lines = []
    for name in ['oaiso-linux-amd64.tar.gz', 'install.sh', 'sbom.cdx.json']:
        with (output / name).open('rb') as stream:
            lines.append(hashlib.file_digest(stream, 'sha256').hexdigest() + '  ' + name)
    (output / 'SHA256SUMS').write_text('\n'.join(lines) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--require-clean-source', action='store_true', help='Require each binary to identify the clean current Git commit')
    parser.add_argument('--installer', type=Path, default=Path(__file__).resolve().parents[1] / 'install.sh')
    args = parser.parse_args()
    package(args.bundle, args.output, args.version, args.installer, args.require_clean_source)
