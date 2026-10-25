#!/usr/bin/env python3
"""Connect reviewed static contracts to isolated CLI fixtures and real Kata.

Never updates pins/baselines, credentials, host services or release artifacts.
Run in a disposable candidate checkout with prepared, matching test assets.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
CLI_TESTS = {"TestCodexCLIWorkerCredentialPrivacy", "TestClaudeCLIIntegration"}
KATA_TESTS = {"TestKataNetworkRevocation",
              "TestKataNetworkRevocation/diagnostic-capture-export",
              "TestKataNetworkRevocation/broker-configuration-scope-and-private-file",
              "TestKataNetworkRevocation/broker-configuration-scope-and-private-file/candidate-agent-clis",
              "TestKataNetworkRevocation/execution-usage-persistence"}


def sha(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def pins_from(dockerfile):
    result = {}
    for agent, arg in (("codex", "CODEX_CLI_VERSION"), ("claude", "CLAUDE_CLI_VERSION")):
        match = re.search(r"^ARG " + arg + r"=(\d+\.\d+\.\d+)$", dockerfile, re.MULTILINE)
        if not match:
            raise ValueError("missing exact worker CLI pin")
        result[agent] = match[1]
    return result


def validate_candidate(candidate, pins):
    if candidate.get("schema") != 1 or candidate.get("errors"):
        raise ValueError("incomplete candidate capture")
    for agent, pin in pins.items():
        version = candidate.get("sources", {}).get(agent + "-cli", {}).get("version", "")
        if not re.search(r"(?<![\d.])" + re.escape(pin) + r"(?![\d.])", version):
            raise ValueError("candidate CLI versions differ from this checkout's pins")


def cli_results(path):
    passed = set()
    for line in Path(path).read_text().splitlines():
        event = json.loads(line)
        if event.get("Action") in ("skip", "fail"):
            raise ValueError("CLI fixture failed or skipped")
        if event.get("Action") == "pass" and event.get("Test"):
            passed.add(event["Test"])
    if not CLI_TESTS <= passed:
        raise ValueError("required CLI fixtures did not run")


def kata_results(text):
    passed = set(re.findall(r"^\s*--- PASS: ([^ ]+) \(", text, re.MULTILINE))
    if re.search(r"^\s*--- (?:FAIL|SKIP):", text, re.MULTILINE) or not KATA_TESTS <= passed:
        raise ValueError("required real-Kata cases failed, skipped or did not run")
    if "CANDIDATE_CODEX_SSE_PASS" not in text or "CANDIDATE_CLAUDE_TOOL_SSE_PASS" not in text:
        raise ValueError("candidate CLI inference fixtures were not confirmed in Kata")


def clean_env(home):
    env = {"HOME": str(home), "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
           "CGO_ENABLED": "0", "GOPROXY": "off", "GOMAXPROCS": "2",
           "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost",
           "OTEL_SDK_DISABLED": "true"}
    # Reuse build/module caches, never credential homes or proxy credentials.
    for key in ("GOCACHE", "GOMODCACHE", "GOROOT"):
        result = subprocess.run(["go", "env", key], capture_output=True, text=True, check=True)
        env[key] = result.stdout.strip()
    return env


def run_logged(argv, path, env, timeout):
    with Path(path).open("w") as output:
        result = subprocess.run([str(v) for v in argv], cwd=ROOT, env=env,
                                stdout=output, stderr=subprocess.STDOUT,
                                timeout=timeout, check=False)
    return result.returncode


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "run"))
    parser.add_argument("--candidate", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--baseline", type=Path, default=ROOT / "compat/upstream/baseline.json")
    parser.add_argument("--cache-dir", type=Path, default=ROOT / ".kata-verification/upstream-contract-cache")
    parser.add_argument("--reviewed-static-sha256", default="")
    parser.add_argument("--codex", type=Path)
    parser.add_argument("--claude", type=Path)
    for flag in ("bundle", "next-bundle", "images", "cache"):
        parser.add_argument("--" + flag, type=Path)
    for flag in ("proxy-image", "network-proxy-image", "worker-image", "node-image"):
        parser.add_argument("--" + flag)
    args = parser.parse_args()
    output = args.out.resolve()
    scratch = ROOT / ".kata-verification"
    if not output.is_relative_to(scratch) or output == scratch:
        parser.error("--out must be a new directory below this checkout's .kata-verification")
    try:
        output.mkdir(parents=True, mode=0o700, exist_ok=False)
    except OSError:
        parser.error("--out must be a new writable qualification directory")
    report = {"schema": 1, "status": "pending", "scope": "synthetic-fixtures-and-real-kata",
              "created_at": datetime.now(timezone.utc).isoformat(), "adopted": False,
              "stages": {"static": "running", "cli": "not-run", "kata": "not-run"},
              "artifacts": {}, "limitations": ["No live provider login/inference/billing or WSL validation"]}
    try:
        report["qualifier_sha256"] = sha(Path(__file__))
        report["source_commit"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        diff = subprocess.check_output(["git", "diff", "HEAD", "--binary"], cwd=ROOT)
        report["tracked_patch_sha256"] = hashlib.sha256(diff).hexdigest()
        pins = pins_from((ROOT / "docker/sandbox/Dockerfile").read_text())
        report["pins"] = pins
        report["artifacts"]["candidate"] = sha(args.candidate)
        static = output / "static.json"
        code = run_logged([sys.executable, ROOT / "scripts/upstream-contracts.py", "compare",
                           "--candidate", args.candidate.resolve(), "--baseline", args.baseline.resolve(),
                           "--cache-dir", args.cache_dir.resolve(), "--json", static,
                           "--markdown", output / "static.md"], output / "static.log", os.environ.copy(), 600)
        if code not in (0, 1) or not static.exists():
            raise ValueError("static comparison could not complete")
        static_report = json.loads(static.read_text())
        if static_report.get("status") not in ("pass", "hold"):
            raise ValueError("static comparison is invalid")
        report["stages"]["static"] = static_report["status"]
        report["static_sha256"] = sha(static)
        try:
            validate_candidate(json.loads(args.candidate.read_text()), pins)
        except ValueError:
            report["status"] = "hold"
            report["reason"] = "Use an isolated candidate checkout with matching exact CLI pins; current pins are unchanged"
            return 1
        if static_report["status"] == "hold" and args.reviewed_static_sha256 != sha(static):
            report["status"] = "hold"
            report["reason"] = "Review static.md; rerun with its exact static.json SHA-256 after resolving findings"
            return 1
        if args.action == "plan":
            report["reason"] = "Plan only; CLI/runtime compatibility remains untested"
            return 0
        required = (args.codex, args.claude, args.bundle, args.next_bundle, args.images,
                    args.cache, args.proxy_image, args.network_proxy_image, args.worker_image, args.node_image)
        if not all(required):
            raise ValueError("run requires candidate binaries and all isolated Kata test assets")
        for key, path in (("codex", args.codex), ("claude", args.claude), ("images", args.images),
                          ("bundle_manifest", args.bundle / "bundle.json"),
                          ("next_bundle_manifest", args.next_bundle / "bundle.json")):
            report["artifacts"][key] = sha(path)
        with tempfile.TemporaryDirectory(prefix="candidate-home-", dir=output) as temporary:
            env = clean_env(Path(temporary))
            for agent, binary in (("codex", args.codex), ("claude", args.claude)):
                version = subprocess.check_output([str(binary.resolve()), "--version"], env=env, text=True, timeout=30)
                if not re.search(r"(?<![\d.])" + re.escape(pins[agent]) + r"(?![\d.])", version):
                    raise ValueError("candidate binary version does not match capture/pin")
            env["CXD_TEST_CODEX_BINARY"] = str(args.codex.resolve())
            env["CXD_TEST_CLAUDE_BINARY"] = str(args.claude.resolve())
            report["stages"]["cli"] = "running"
            cli_log = output / "cli.jsonl"
            if run_logged(["go", "test", "-json", "-count=1", "-run", "^(" + "|".join(sorted(CLI_TESTS)) + ")$",
                           "./internal/authproxy"], cli_log, env, 600):
                raise ValueError("candidate CLI fixtures failed")
            cli_results(cli_log)
            report["stages"]["cli"] = "pass"
            acceptance = output / "acceptance.test"
            # Build the tests from this checkout; a supplied PASS log/test binary
            # cannot stand in for execution of the fixed acceptance suite.
            manifest = json.loads((args.bundle / "bundle.json").read_text())
            version = manifest.get("version", "")
            if not re.fullmatch(r"[A-Za-z0-9._+-]{1,128}", version):
                raise ValueError("invalid bundle build version")
            if run_logged(["go", "test", "-c", "-ldflags", "-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=" + version,
                           "-o", acceptance, "./internal/sandbox"], output / "compile.log", env, 600):
                raise ValueError("candidate acceptance compilation failed")
            report["artifacts"]["acceptance"] = sha(acceptance)
            report["stages"]["kata"] = "running"
            env["CODEX_DOCK_DISPOSABLE_SETUP"] = "1"
            argv = [sys.executable, ROOT / "scripts/verify-setup.py", "--acceptance", acceptance,
                    "--acceptance-run", "^TestKataNetworkRevocation$", "--offline"]
            for flag, value in (("bundle", args.bundle), ("next-bundle", args.next_bundle),
                                ("images", args.images), ("cache", args.cache), ("image", args.node_image),
                                ("proxy-image", args.proxy_image), ("network-proxy-image", args.network_proxy_image),
                                ("worker-image", args.worker_image)):
                argv += ["--" + flag, str(value.resolve()) if isinstance(value, Path) else value]
            if run_logged(argv, output / "kata-controller.log", env, 3600):
                raise ValueError("isolated Kata/setup/update/recovery failed")
            matches = re.findall(r"Evidence: (.+)", (output / "kata-controller.log").read_text())
            if len(matches) != 1:
                raise ValueError("missing Kata evidence directory")
            evidence = Path(matches[0]).resolve()
            if not evidence.is_relative_to(ROOT / ".kata-verification/setup-e2e"):
                raise ValueError("unexpected Kata evidence location")
            kata_results((evidence / "acceptance.txt").read_text())
            report["artifacts"]["kata_acceptance"] = sha(evidence / "acceptance.txt")
            report["kata_evidence"] = str(evidence.relative_to(ROOT))
            report["stages"]["kata"] = "pass"
            report["status"] = "pass"
        return 0
    except (ValueError, OSError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        report["status"] = "error"
        # Logs stay private; report no credential-bearing exception/argv/output.
        report["reason"] = str(error) if isinstance(error, ValueError) else type(error).__name__
        return 2
    finally:
        for stage, status in report["stages"].items():
            if status == "running":
                report["stages"][stage] = "error"
        (output / "qualification.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
        print("Candidate qualification:", report["status"], "(pins and baseline unchanged)")


if __name__ == "__main__":
    sys.exit(main())
