"""Discover actions, cancel safely, select by keyboard, and resize a real PTY."""
import fcntl
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import termios
import time

root = Path(sys.argv[2])
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestDashboardActionsPTYHelper$"],
    stdin=slave, stdout=slave, stderr=slave, env=os.environ | {"TERM": "xterm-256color", "OAISO_ACTIONS_PTY_HELPER": str(root)},
    start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        output.extend(os.read(master, 65536))
        del output[:-65536]


def expect(marker):
    deadline = time.monotonic() + 10
    while marker not in output and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in output, (marker, output[-2500:])
    output.clear()


def send(value):
    os.write(master, value)


try:
    expect(b"Actions F2")
    send(b"\x1bOQ")
    expect(b"New sandbox")
    assert not (root / "selected").exists()
    send(b"\x1b")
    expect(b"STATUS")
    assert not (root / "selected").exists(), "Esc triggered an action"
    send(b"\t\r")  # discover the same menu through its focusable button
    expect(b"New sandbox")
    send(b"\x1b[B\r")
    expect(b"STATUS")
    assert (root / "selected").read_text() == "c"
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 22, 60, 0, 0))
    os.kill(process.pid, 28)  # SIGWINCH
    send(b" ")
    expect(b"New sandbox")
    send(b"F")  # upper-case shortcuts also work inside the menu
    expect(b"STATUS")
    assert (root / "selected").read_text() == "f"
    send(b"q")
    deadline = time.monotonic() + 5
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, output[-2500:]
    assert termios.tcgetattr(slave) == previous
    print("PASS: discoverable Actions button, F2/Space, arrows/Enter, shortcut selection, Esc cancellation, narrow resize, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
