#!/usr/bin/env bash
# Standalone release installer. This file also works when piped to bash.
set -euo pipefail

main() {
  local operator="" bundle="" offline=false argument uv python bootstrap
  local -a arguments=("$@") python_args
  while (($#)); do
    argument=$1
    case "$argument" in
      --apply|--allow-downgrade) ;;
      --offline) offline=true ;;
      --operator|--bundle|--cache|--version)
        (($# >= 2)) || { echo "install: missing value for $argument" >&2; return 1; }
        case "$argument" in --operator) operator=$2 ;; --bundle) bundle=$2 ;; esac
        shift ;;
      -h|--help)
        printf '%s\n' 'Usage: install.sh [--apply] [--allow-downgrade] [--version TAG] [--operator USER] [--bundle PATH] [--cache PATH] [--offline]' \
          'Downloads a prebuilt Oaiso release and shows its installation plan.' \
          '--apply installs after the plan succeeds. No Go, Make or checkout is needed.' \
          '--bundle uses an existing bundle; --offline requires it and prepared uv/Python.'
        return ;;
      *) echo "install: unknown option: $argument (use --help)" >&2; return 1 ;;
    esac
    shift
  done
  [[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'install: the node requires Ubuntu 26.04 x86_64' >&2; return 1; }
  [[ $(id -u) != 0 || -n $operator ]] || { echo 'install: pass --operator USER when running as root' >&2; return 1; }
  if "$offline" && [[ -z $bundle ]]; then
    echo 'install: --offline requires --bundle PATH; prepare the release and uv/Python first' >&2
    return 1
  fi
  if [[ -z $bundle ]]; then
    command -v gh >/dev/null || { echo 'install: signed releases require GitHub CLI (gh): https://github.com/cli/cli/blob/trunk/docs/install_linux.md' >&2; return 1; }
  fi
  if ! uv=$(command -v uv); then
    if [[ -x "$HOME/.local/bin/uv" ]]; then
      uv="$HOME/.local/bin/uv"
    else
      "$offline" && { echo 'install: offline setup requires prepared uv and Python 3.12' >&2; return 1; }
      [[ ! -e "$HOME/.local/bin/uv" && ! -L "$HOME/.local/bin/uv" ]] || { echo 'install: ~/.local/bin/uv exists but is not executable' >&2; return 1; }
      command -v curl >/dev/null || { echo 'install: curl is required' >&2; return 1; }
      bootstrap=$(mktemp)
      if ! curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        https://astral.sh/uv/0.13.5/install.sh -o "$bootstrap"; then
        rm -f "$bootstrap"; return 1
      fi
      if ! UV_INSTALL_DIR="$HOME/.local/bin" UV_NO_MODIFY_PATH=1 sh "$bootstrap"; then
        rm -f "$bootstrap"; return 1
      fi
      rm -f "$bootstrap"
      uv="$HOME/.local/bin/uv"
    fi
  fi
  python_args=(run --no-project --no-config --isolated --managed-python --python 3.12 --system-certs)
  if "$offline"; then python_args+=(--offline --no-python-downloads); fi
  python=$("$uv" "${python_args[@]}" python -I -c 'import os,sys; print(os.path.realpath(sys._base_executable))')
  [[ $python = /* && -x $python ]] || { echo 'install: uv did not select a Python executable' >&2; return 1; }
  "$python" -I - "${arguments[@]}" <<'PYINSTALLER'
import argparse
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import ssl
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import urllib.request

REPOSITORY = 'pacificbelt30/sandbox-oasis'
DEFAULT_VERSION = None  # release packaging sets this value
ARCHIVE = 'oaiso-linux-amd64.tar.gz'
FILES = {'oaiso', 'codex-dock-worker-guard', 'codex-dock-io-logger'}
DATA_FILES = {'bundle.json', 'LICENSE', 'THIRD_PARTY_NOTICES.txt'}
TAG = re.compile(r'v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?\Z')


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != 'https':
            raise ValueError('refusing a non-HTTPS release redirect')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def download(url, target, limit):
    if urllib.parse.urlsplit(url).scheme != 'https':
        raise ValueError('release downloads require HTTPS')
    ca = os.environ.get('SSL_CERT_FILE') or '/etc/ssl/certs/ca-certificates.crt'
    opener = urllib.request.build_opener(HTTPSRedirect(), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=ca)))
    request = urllib.request.Request(url, headers={'User-Agent': 'oaiso-installer', 'Accept': 'application/vnd.github+json'})
    with opener.open(request, timeout=60) as response, target.open('xb') as output:
        total = 0
        while chunk := response.read(1 << 20):
            total += len(chunk)
            if total > limit:
                raise ValueError('release download exceeds its size limit')
            output.write(chunk)


def validate_bundle(directory, version=None):
    manifest = directory / 'bundle.json'
    if manifest.is_symlink() or not manifest.is_file() or manifest.stat().st_size > 1 << 20:
        raise ValueError('invalid bundle.json')
    record = json.loads(manifest.read_text())
    if not isinstance(record, dict) or record.get('schema') != 1 or not isinstance(record.get('files'), dict) or set(record['files']) != FILES:
        raise ValueError('unsupported or incomplete bundle')
    if not isinstance(record.get('version'), str) or not TAG.fullmatch(record['version']) or version and record['version'] != version:
        raise ValueError('bundle version does not match the selected release')
    for name, checksum in record['files'].items():
        path = directory / name
        if path.is_symlink() or not path.is_file() or not os.access(path, os.X_OK):
            raise ValueError('missing or invalid bundle executable: ' + name)
        with path.open('rb') as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != checksum:
                raise ValueError('bundle checksum mismatch: ' + name)
    return record


def unpack(archive, destination):
    seen = set()
    size = 0
    with tarfile.open(archive, 'r:gz') as source:
        for member in source:
            if member.name not in FILES | DATA_FILES or member.name in seen or not member.isfile():
                raise ValueError('unexpected path, duplicate or special file in release archive')
            seen.add(member.name)
            size += member.size
            if member.size < 0 or size > 512 << 20 or member.name == 'bundle.json' and member.size > 1 << 20:
                raise ValueError('release archive exceeds its expanded size limit')
            if member.name in DATA_FILES and member.size > 8 << 20:
                raise ValueError('release notice exceeds its size limit')
            with source.extractfile(member) as incoming, (destination / member.name).open('xb') as output:
                shutil.copyfileobj(incoming, output)
            (destination / member.name).chmod(0o755 if member.name in FILES else 0o644)
    if seen != FILES | DATA_FILES:
        raise ValueError('incomplete release archive')


def verify_attestation(artifact, bundle, version):
    verifier = shutil.which('gh')
    if not verifier:
        raise ValueError('signed release verification requires GitHub CLI (gh): https://github.com/cli/cli/blob/trunk/docs/install_linux.md')
    subprocess.run([verifier, 'attestation', 'verify', str(artifact), '--bundle', str(bundle),
                    '--repo', REPOSITORY,
                    '--source-ref', 'refs/tags/' + version,
                    '--cert-identity', 'https://github.com/' + REPOSITORY + '/.github/workflows/release.yml@refs/tags/' + version,
                    '--cert-oidc-issuer', 'https://token.actions.githubusercontent.com',
                    '--deny-self-hosted-runners', '--hostname', 'github.com'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)


def get_release(work, version):
    if not version:
        metadata = work / 'release.json'
        download('https://api.github.com/repos/' + REPOSITORY + '/releases/latest', metadata, 1 << 20)
        version = json.loads(metadata.read_text())['tag_name']
    if not isinstance(version, str) or not TAG.fullmatch(version):
        raise ValueError('version must be a release tag such as v0.0.2')
    base = 'https://github.com/' + REPOSITORY + '/releases/download/' + urllib.parse.quote(version, safe='') + '/'
    checksums, archive = work / 'SHA256SUMS', work / ARCHIVE
    attestation = work / 'provenance.json'
    download(base + 'provenance.json', attestation, 4 << 20)
    download(base + 'SHA256SUMS', checksums, 1 << 20)
    matches = [line.split()[0] for line in checksums.read_text().splitlines()
               if len(line.split()) == 2 and line.split()[1] == ARCHIVE]
    if len(matches) != 1 or not re.fullmatch('[0-9a-f]{64}', matches[0]):
        raise ValueError('release checksum entry is missing or ambiguous')
    download(base + ARCHIVE, archive, 256 << 20)
    with archive.open('rb') as stream:
        if hashlib.file_digest(stream, 'sha256').hexdigest() != matches[0]:
            raise ValueError('release archive checksum mismatch')
    verify_attestation(archive, attestation, version)
    directory = work / 'bundle'
    directory.mkdir()
    unpack(archive, directory)
    validate_bundle(directory, version)
    return directory


def main():
    parser = argparse.ArgumentParser(description='Install a verified prebuilt Oaiso node release')
    parser.add_argument('--version')
    parser.add_argument('--bundle', type=Path)
    parser.add_argument('--operator', default=pwd.getpwuid(os.getuid()).pw_name)
    parser.add_argument('--cache', type=Path)
    parser.add_argument('--offline', action='store_true')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--allow-downgrade', action='store_true')
    args = parser.parse_args()
    if args.version and args.bundle:
        parser.error('--version and --bundle cannot be combined')
    with tempfile.TemporaryDirectory(prefix='oaiso-release-') as temporary:
        directory = args.bundle.resolve() if args.bundle else get_release(Path(temporary), args.version or DEFAULT_VERSION)
        record = validate_bundle(directory, args.version)
        cli = directory / 'oaiso'
        actual = subprocess.check_output([str(cli), '--version'], text=True).strip()
        if actual != 'oaiso version ' + record['version']:
            raise ValueError('CLI version does not match bundle.json')
        command = [str(cli), 'setup', '--bundle', str(directory), '--operator', args.operator, '--python', sys.executable]
        if args.cache:
            command += ['--cache', str(args.cache)]
        if args.allow_downgrade:
            command += ['--allow-downgrade']
        if args.offline:
            command += ['--offline']
        subprocess.run(command, check=True)
        if args.apply:
            apply = command + ['--apply']
            if os.geteuid() != 0:
                apply = ['sudo', '--', *apply]
            subprocess.run(apply, check=True)
            print('Node installed. Log in again, then run oaiso init.')
        else:
            print('Plan only. Re-run the installer with --apply to install this release.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.SubprocessError) as error:
        print('install: ' + str(error), file=sys.stderr)
        sys.exit(1)
PYINSTALLER
}

main "$@"
