"""Candidate qualification must never turn skip/mismatch into adoption."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("qualify", Path(__file__).with_name("qualify-upstream.py"))
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class QualificationTests(unittest.TestCase):
    def run_plan(self, root, name, static_status="pass", versions=None, extra=()):
        dockerfile = root / "docker/sandbox/Dockerfile"
        dockerfile.parent.mkdir(parents=True, exist_ok=True)
        dockerfile.write_text("ARG CODEX_CLI_VERSION=1.2.3\nARG CLAUDE_CLI_VERSION=4.5.6\n")
        candidate = root / "candidate.json"
        candidate.write_text(json.dumps({"schema": 1, "sources": versions or {
            "codex-cli": {"version": "codex-cli 1.2.3"}, "claude-cli": {"version": "4.5.6 (Claude Code)"}}}))
        output = root / ".kata-verification" / name

        def static(argv, *unused):
            Path(argv[argv.index("--json") + 1]).write_text(json.dumps({"status": static_status}))
            return 0 if static_status == "pass" else 1

        argv = ["qualify-upstream.py", "plan", "--candidate", str(candidate), "--out", str(output), *extra]
        before = dockerfile.read_bytes()
        with patch.object(CHECK, "ROOT", root), patch.object(CHECK.sys, "argv", argv), \
                patch.object(CHECK.subprocess, "check_output", side_effect=["fixture-commit", b""]), \
                patch.object(CHECK, "run_logged", side_effect=static) as runner:
            code = CHECK.main()
        self.assertEqual(runner.call_count, 1, "plan invoked a runtime stage")
        self.assertEqual(dockerfile.read_bytes(), before, "qualification adopted candidate pins")
        return code, json.loads((output / "qualification.json").read_text()), output

    def test_plan_does_not_claim_runtime_compatibility_or_adopt_pins(self):
        with tempfile.TemporaryDirectory() as temp:
            code, result, _ = self.run_plan(Path(temp), "plan")
        self.assertEqual(code, 0)
        self.assertEqual(result["status"], "pending")
        self.assertEqual(result["stages"], {"static": "pass", "cli": "not-run", "kata": "not-run"})
        self.assertFalse(result["adopted"])

    def test_static_hold_requires_exact_reviewed_digest(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            code, result, output = self.run_plan(root, "held", "hold")
            self.assertEqual((code, result["status"]), (1, "hold"))
            digest = CHECK.sha(output / "static.json")
            code, result, _ = self.run_plan(root, "wrong", "hold", extra=("--reviewed-static-sha256", "0" * 64))
            self.assertEqual((code, result["status"]), (1, "hold"))
            code, result, _ = self.run_plan(root, "reviewed", "hold", extra=("--reviewed-static-sha256", digest))
            self.assertEqual((code, result["status"]), (0, "pending"))
            self.assertFalse(result["adopted"])

    def test_existing_output_is_usage_error_and_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            output = root / ".kata-verification/existing"
            output.mkdir(parents=True)
            marker = output / "qualification.json"
            marker.write_text("previous-result")
            argv = ["qualify-upstream.py", "plan", "--candidate", "/not-read", "--out", str(output)]
            with patch.object(CHECK, "ROOT", root), patch.object(CHECK.sys, "argv", argv), \
                    self.assertRaises(SystemExit) as error:
                CHECK.main()
            self.assertEqual(error.exception.code, 2)
            self.assertEqual(marker.read_text(), "previous-result")

    def test_candidate_must_match_checkout_pins(self):
        pins = CHECK.pins_from("ARG CODEX_CLI_VERSION=1.2.3\nARG CLAUDE_CLI_VERSION=4.5.6\n")
        snapshot = {"schema": 1, "sources": {"codex-cli": {"version": "codex-cli 1.2.3"},
                                              "claude-cli": {"version": "4.5.6 (Claude Code)"}}}
        CHECK.validate_candidate(snapshot, pins)
        snapshot["sources"]["codex-cli"]["version"] = "codex-cli 11.2.3"
        with self.assertRaises(ValueError):
            CHECK.validate_candidate(snapshot, pins)
        with self.assertRaises(ValueError):
            CHECK.pins_from("ARG CODEX_CLI_VERSION=latest\n")

    def test_cli_fixture_missing_skip_and_failure_cannot_pass(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "cli.jsonl"
            events = [{"Action": "pass", "Test": name} for name in CHECK.CLI_TESTS]
            path.write_text("\n".join(json.dumps(v) for v in events))
            CHECK.cli_results(path)
            for invalid in [events[:1], events + [{"Action": "skip", "Test": "required/subcase"}],
                            events + [{"Action": "fail", "Test": "required/subcase"}]]:
                path.write_text("\n".join(json.dumps(v) for v in invalid))
                with self.assertRaises(ValueError):
                    CHECK.cli_results(path)

    def test_kata_requires_named_cases_and_real_agent_markers(self):
        text = "\n".join("--- PASS: " + name + " (1.00s)" for name in CHECK.KATA_TESTS)
        with self.assertRaises(ValueError):
            CHECK.kata_results(text)
        text += "\nCANDIDATE_CODEX_SSE_PASS\nCANDIDATE_CLAUDE_TOOL_SSE_PASS\n"
        CHECK.kata_results(text)
        with self.assertRaises(ValueError):
            CHECK.kata_results(text + "--- SKIP: some-case (0.00s)\n")

    def test_fixture_environment_excludes_host_credentials_and_config(self):
        class Result:
            stdout = "/build-cache\n"
        with patch.dict(os.environ, {"OPENAI_API_KEY": "private", "ANTHROPIC_API_KEY": "private",
                                    "SSH_AUTH_SOCK": "/private/socket", "CODEX_HOME": "/private/home",
                                    "HTTPS_PROXY": "http://user:private@proxy"}), \
                patch.object(CHECK.subprocess, "run", return_value=Result()):
            env = CHECK.clean_env(Path("/isolated-home"))
        self.assertEqual(env["HOME"], "/isolated-home")
        for key in ("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "SSH_AUTH_SOCK", "CODEX_HOME", "HTTPS_PROXY"):
            self.assertNotIn(key, env)


if __name__ == "__main__":
    unittest.main()
