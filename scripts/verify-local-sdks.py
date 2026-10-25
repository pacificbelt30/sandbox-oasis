#!/usr/bin/env python3
"""Run both local SDK contracts against the same Go adapter and fake backend."""
import argparse
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture', type=Path, required=True, help='go test -c ./pkg/cxd/localapi')
    args = parser.parse_args()
    binary = args.fixture.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='oaiso-sdk-') as directory:
        wrapper = Path(directory) / 'oaiso-fixture'
        wrapper.write_text('#!/bin/sh\nexec ' + shlex.quote(str(binary)) + " '-test.run=^TestSDKFixture$'\n")
        wrapper.chmod(0o700)
        env = dict(os.environ, OAISO_SDK_FIXTURE='1', OAISO_SDK_TEST_BINARY=str(wrapper),
                   PYTHONPATH=str(ROOT / 'sdk/python'))
        subprocess.run([sys.executable, '-m', 'unittest', 'discover', '-v', '-s', str(ROOT / 'sdk/python/tests')],
                       env=env, check=True, timeout=120)
        subprocess.run(['npm', 'test'], cwd=ROOT / 'sdk/typescript', env=env, check=True, timeout=120)
    print('PASS: Python and TypeScript contract, retry, cancellation, redaction and cleanup')


if __name__ == '__main__':
    main()
