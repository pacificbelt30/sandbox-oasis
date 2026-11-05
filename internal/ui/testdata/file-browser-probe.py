"""Exercise browser navigation failures, refresh and parent selection over a PTY."""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import termios
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "sandbox" / "testdata"))
from terminal_screen import Screen

root = Path(sys.argv[2])
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestFileBrowserPTYHelper$"],
    stdin=slave, stdout=slave, stderr=slave,
    env=os.environ | {"TERM": "xterm-256color", "OAISO_FILES_PTY_HELPER": str(root)},
    start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
screen = Screen(24, 80)
sent_at = -1


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        screen.feed(os.read(master, 65536))


def expect(marker):
    deadline = time.monotonic() + 8
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text())


def send(value):
    global sent_at
    sent_at = screen.received
    os.write(master, value)


def wait_page(page, count=0):
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        try:
            state = json.loads((root / "state.json").read_text())
            if state["lists"] >= count and not state["busy"] and state["page"] == page:
                return
        except (FileNotFoundError, json.JSONDecodeError):
            pass
        pump()
    raise AssertionError("directory read did not finish")


try:
    wait_page("files", 1)
    expect(b"root.txt")
    send(b"\r")
    wait_page("message", 2)
    expect(b"gone: directory no longer")
    assert b"cannot open /workspace/" in screen.text()
    send(b"\r")
    wait_page("files", 2)
    expect(b"root.txt")
    send(b"\x1b[B\x1b[B\r")
    wait_page("text", 2)
    expect(b"literal file contents")
    assert json.loads((root / "reads.json").read_text()) == ["root.txt"]
    send(b"\x1b")
    wait_page("files", 2)
    expect(b"root.txt")
    send(b"g")
    wait_page("files", 3)
    expect(b"root.txt")
    send(b"\r")
    wait_page("text", 3)
    expect(b"literal file contents")
    assert json.loads((root / "reads.json").read_text()) == ["root.txt", "root.txt"]
    send(b"\x1b")
    wait_page("files", 3)
    expect(b"root.txt")
    send(b"\x1b[A\r")
    wait_page("files", 4)
    expect(b"child.txt")
    send(b"\x7f")
    wait_page("files", 5)
    expect(b"root.txt")
    send(b"\r")
    wait_page("files", 6)
    expect(b"child.txt")  # Backspace kept z selected rather than switching to gone.
    send(b"\x1b")
    deadline = time.monotonic() + 5
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, screen.text()
    assert termios.tcgetattr(slave) == previous
    print("PASS: failed navigation retained the root path, refresh retained file selection, parent retained directory selection, Esc exit, terminal restoration")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
