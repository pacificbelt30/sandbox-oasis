"""Exercise the pinned agent UIs offline with synthetic credentials.

No prompt is submitted to a provider. A loopback fixture handles startup reads.
"""
import ctypes
import fcntl
import http.server
import json
import os
import pathlib
import pty
import re
import select
import signal
import sqlite3
import struct
import subprocess
import tempfile
import termios
import threading
import time

assert ctypes.CDLL(None).prctl(38, 1, 0, 0, 0) == 0


class Fixture(http.server.BaseHTTPRequestHandler):
    posts = 0

    def log_message(self, *args):
        pass

    def do_GET(self):
        data = json.dumps({"object": "list", "data": [{"id": "gpt-5.4", "object": "model"}, {"id": "claude-sonnet-4-6", "object": "model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        # No model turn is part of a keyboard/presentation test.
        Fixture.posts += 1
        self.send_error(503, "interactive test does not submit model turns")


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = "http://127.0.0.1:%d" % server.server_port
resume = os.environ.get("OAISO_PROBE_CODEX_RESUME") == "1"
agents = [("codex", "0.163.0")] if resume else [("codex", "0.163.0"), ("claude", "2.1.306")]
for agent, expected in agents:
    assert expected in subprocess.check_output([agent, "--version"], text=True)
    with tempfile.TemporaryDirectory(prefix="agent-ui-", dir=os.environ["HOME"]) as directory:
        home = pathlib.Path(directory)
        environment = {key: value for key, value in os.environ.items()
                       if key in ("PATH", "LANG", "LC_ALL", "TERM")}
        environment.update(HOME=directory, TERM="xterm-256color", NO_PROXY="127.0.0.1,localhost",
                           OPENAI_API_KEY="sk-ui-fixture-never-valid", ANTHROPIC_API_KEY="sk-ui-fixture-never-valid",
                           ANTHROPIC_BASE_URL=base, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
                           DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
        if agent == "codex":
            # Preserve the product-selected persistent database mount while
            # keeping this synthetic UI's DB separate from other fixture runs.
            state = home
            if os.environ.get("CODEX_SQLITE_HOME"):
                state = pathlib.Path(os.environ["CODEX_SQLITE_HOME"])
                if not resume:
                    state = state / ("ui-" + home.name)
                state.mkdir(parents=True, mode=0o700, exist_ok=True)
                environment["CODEX_SQLITE_HOME"] = str(state)
            if resume:
                environment["CODEX_HOME"] = str(pathlib.Path(os.environ["HOME"]) / ".codex")
            with sqlite3.connect(state / "wal-preflight.sqlite") as database:
                assert database.execute("PRAGMA journal_mode=WAL").fetchone()[0] == "wal"
                database.execute("CREATE TABLE probe (value TEXT)")
            print("PASS: product-selected Codex database filesystem supports SQLite WAL", flush=True)
            config = home / ".codex"
            config.mkdir(mode=0o700)
            original = pathlib.Path(os.environ["HOME"]) / ".codex/config.toml"
            # Keep the product-generated sandbox posture, then route startup
            # HTTP only to the local fixture and trust this test workspace.
            sandbox = next(line for line in original.read_text().splitlines() if line.startswith("sandbox_mode ="))
            (config / "config.toml").write_text(sandbox + '\ncheck_for_update_on_startup=false\nanalytics.enabled=false\notel.metrics_exporter="none"\n[projects."/workspace"]\ntrust_level="trusted"\n')
            args = ["codex", "--no-alt-screen", "--no-daemon", "-C", "/workspace", "-m", "gpt-5.4",
                    "-c", 'model_provider="fixture"', "-c", 'features.apps=false',
                    "-c", 'features.responses_websockets=false', "-c", 'features.responses_websockets_v2=false',
                    "-c", 'check_for_update_on_startup=false', "-c", 'analytics.enabled=false', "-c", 'otel.metrics_exporter="none"',
                    "-c", 'model_providers.fixture={name="fixture",base_url="' + base + '/v1",wire_api="responses"}']
            if resume:
                # The seed is an actual `codex exec` session. Upstream excludes
                # non-interactive sources from --last unless explicitly asked.
                args += ["resume", "--last", "--include-non-interactive"]
        else:
            settings = {"hasCompletedOnboarding": True, "theme": "dark",
                        "customApiKeyResponses": {"approved": [environment["ANTHROPIC_API_KEY"][-20:]], "rejected": []},
                        "projects": {"/workspace": {"hasTrustDialogAccepted": True, "hasCompletedProjectOnboarding": True}}}
            (home / ".claude.json").write_text(json.dumps(settings))
            args = ["claude", "--model", "claude-sonnet-4-6", "--permission-mode", "manual"]
        master, slave = pty.openpty()
        before = termios.tcgetattr(slave)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        process = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave, cwd="/workspace", env=environment,
                                   start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
        output = bytearray()
        # A PTY is not a terminal emulator. Reply to the agents' capability
        # queries so keyboard negotiation cannot consume subsequent keypresses.
        # Model an ordinary xterm with no enhanced keyboard flags enabled.
        replies = {
            b"\x1b[c": b"\x1b[?1;2c",
            b"\x1b[>0q": b"\x1bP>|XTerm(390)\x1b\\",
            b"\x1b[?u": b"\x1b[?0u",
            b"\x1b[6n": b"\x1b[1;1R",
            b"\x1b]10;?\x1b\\": b"\x1b]10;rgb:ffff/ffff/ffff\x1b\\",
            b"\x1b]11;?\x1b\\": b"\x1b]11;rgb:0000/0000/0000\x1b\\",
        }
        answered = dict.fromkeys(replies, 0)

        def drain(seconds):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], .05)
                if ready:
                    output.extend(os.read(master, 65536))
                    for query, reply in replies.items():
                        count = output.count(query)
                        if count > answered[query] and process.poll() is None:
                            os.write(master, reply * (count - answered[query]))
                            answered[query] = count
                if process.poll() is not None:
                    break

        try:
            # Cold-started runtimes on virtiofs can take several seconds to
            # load. Wait for observable rendering, with a bounded deadline,
            # rather than treating a fixed sleep as a readiness signal.
            ready_deadline = time.monotonic() + 30
            while b"\x1b[" not in output and process.poll() is None and time.monotonic() < ready_deadline:
                drain(.2)
            assert process.poll() is None, "agent exited before input: %s" % process.returncode
            assert b"\x1b[" in output, "agent did not render a terminal UI"
            if resume:
                deadline = time.monotonic() + 30
                folder_trusted = False
                def resumed_text():
                    return re.sub(rb"\s+", b"", re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output))
                while b"fixturecomplete" not in resumed_text() and time.monotonic() < deadline:
                    # The resumed product HOME has not trusted this synthetic
                    # workspace. Exercise its explicit first-run confirmation,
                    # retaining the product-generated permission settings.
                    if b"Folder access" in output and not folder_trusted:
                        os.write(master, b"\r")
                        folder_trusted = True
                    drain(.2)
                assert b"fixturecomplete" in resumed_text(), "saved fixture history not rendered after VM restart"
            drain(.5)
            os.write(master, b"\x1b[200~" + "日本語UI確認".encode() + b"\x1b[201~")
            paste_deadline = time.monotonic() + 10
            text = b""
            while "日本語".encode() not in text and process.poll() is None and time.monotonic() < paste_deadline:
                drain(.2)
                text = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output)
            assert "日本語".encode() in text, "Japanese paste not rendered"
            os.write(master, bytes([27]))
            drain(.3)
            assert process.poll() is None, "Esc unexpectedly exited the agent"
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
            os.kill(process.pid, signal.SIGWINCH)
            drain(.5)
            # Clear/cancel the unsent input, then exit via the agent's own
            # Ctrl-C handler. Separate presses avoid key-repeat aggregation.
            exit_deadline = time.monotonic() + 8
            while process.poll() is None and time.monotonic() < exit_deadline:
                os.write(master, bytes([3]))
                drain(.4)
            assert process.poll() is not None, "agent did not exit through Ctrl-C"
            assert process.returncode in (0, 130), process.returncode
            assert termios.tcgetattr(slave) == before, "agent left raw terminal flags behind"
            assert b"sk-ui-fixture-never-valid" not in output, "credential displayed"
            print("PASS: fixed %s %s interactive ANSI UI, Japanese paste, Esc, resize, Ctrl-C exit, terminal restoration" % (agent, expected), flush=True)
            if resume:
                print("PASS: actual Codex resume --last --include-non-interactive displayed the saved exec fixture history after VM restart", flush=True)
        except BaseException:
            print("AGENT_UI_FAILURE", agent, repr(output[-12000:]), flush=True)
            raise
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            os.close(master)
            os.close(slave)
assert Fixture.posts == 0, "a model turn was submitted"
server.shutdown()
