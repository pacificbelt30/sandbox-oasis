#!/usr/bin/env python3
"""Run public Go API acceptance on a prepared, explicitly selected Kata node.

Builds a test-only auth proxy with synthetic upstreams; never deploy that image.
The caller supplies the node, base proxy/worker images and standalone BuildKit.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import uuid


def run_acceptance(command, repo, env):
    """Stream readable Go output and require an executed API subtest."""
    passed = set()
    with subprocess.Popen(command, cwd=repo, env=env, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True) as process:
        for line in process.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line, end="", flush=True)
                continue
            if "Output" in event:
                print(event["Output"], end="", flush=True)
            if event.get("Action") == "pass" and event.get("Test", "").startswith("TestKataAPIIntegration/"):
                passed.add(event["Test"])
        code = process.wait()
    if code:
        raise subprocess.CalledProcessError(code, command)
    if not passed:
        raise RuntimeError("no API acceptance subtests passed; check --run and optional fixture settings")
    return passed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--run", default="^TestKataAPIIntegration$", help="Go test name/subtest expression")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    binary = str(Path(args.binary).resolve())
    base = os.environ.get("CODEX_DOCK_KATA_PROXY_IMAGE", "codex-dock-proxy:latest")
    if not re.fullmatch(r"[A-Za-z0-9_./:@-]+", base):
        parser.error("invalid base proxy image reference")
    if not re.fullmatch(r"[A-Za-z0-9_.+-]+", args.version):
        parser.error("invalid build version")
    root_test = args.run.split("/", 1)[0]
    if root_test not in {"^TestKataAPIIntegration$", "^TestKataAPIIntegration", "TestKataAPIIntegration"}:
        parser.error("--run must select TestKataAPIIntegration, optionally followed by a subtest expression")
    version_flag = "-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=" + args.version
    image = "codex-dock-proxy:api-fixture-" + uuid.uuid4().hex[:12]
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="amd64")
    with tempfile.TemporaryDirectory(prefix="cxd-api-fixture-") as directory:
        work = Path(directory)
        subprocess.run(["go", "test", "-c", "-ldflags", version_flag, "./internal/authproxy", "-o", str(work / "fixture.test")], cwd=repo, env=env, check=True)
        subprocess.run(["go", "build", "-ldflags", version_flag, "-o", str(work / "embedding"), "./examples/embedding"], cwd=repo, env=env, check=True)
        (work / "Dockerfile").write_text(
            f"FROM {base}\nCOPY --chmod=0755 fixture.test /codex-dock\n"
            'ENV CXD_KATA_FIXTURE_PROXY=1\nLABEL io.codex-dock.proxy-protocol="external-store-v1"\n'
            f'LABEL io.codex-dock.proxy-build="{args.version}"\n'
        )
        subprocess.run([binary, "build", "-t", image, "-f", "Dockerfile"], cwd=work, check=True)
        passed = run_acceptance(["go", "test", "-json", "-ldflags", version_flag, "./pkg/cxd", "-run", args.run, "-count=1", "-v", "-timeout=25m"], repo,
                                dict(os.environ, CODEX_DOCK_KATA_API_E2E="1", CODEX_DOCK_KATA_API_PROXY_IMAGE=image,
                                     CODEX_DOCK_KATA_API_OWN_IMAGE="1", CODEX_DOCK_KATA_EMBEDDING_BINARY=str(work / "embedding"),
                                     CODEX_DOCK_KATA_TEST_BINARY=binary))
        print(f"Verified {len(passed)} API acceptance subtests.", file=sys.stdout)


if __name__ == "__main__":
    main()
