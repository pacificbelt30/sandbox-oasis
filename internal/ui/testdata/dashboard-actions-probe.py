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

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "sandbox" / "testdata"))
from terminal_screen import Screen

root = Path(sys.argv[2])
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestDashboardActionsPTYHelper$"],
    stdin=slave, stdout=slave, stderr=slave, env=os.environ | {"TERM": "xterm-256color", "OAISO_ACTIONS_PTY_HELPER": str(root)},
    start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()
screen = Screen(24, 80)
sent_at = -1


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        data = os.read(master, 65536)
        output.extend(data)
        screen.feed(data)
        del output[:-65536]


def expect(marker):
    deadline = time.monotonic() + 10
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text(), output[-2500:])
    output.clear()


def send(value):
    global sent_at
    sent_at = screen.received
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
    screen = Screen(22, 60)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 22, 60, 0, 0))
    os.kill(process.pid, 28)  # SIGWINCH
    send(b" ")
    expect(b"New sandbox")
    send(b"F")  # upper-case shortcuts also work inside the menu
    expect(b"STATUS")
    assert (root / "selected").read_text() == "f"
    for tabs in range(3):
        send(b" ")
        expect(b"New sandbox")
        send(b"x")
        expect(b"Command")
        send(b"echo must-not-run" + b"\t" * tabs + b"\x1b")
        expect(b"STATUS")
        assert not (root / "executed").exists(), "Esc executed a command"
    send(b" ")
    expect(b"New sandbox")
    send(b"x")
    expect(b"Command")
    command = "printf '日本語 [red]literal'"
    send(b"\x1b[200~" + command.encode() + b"\x1b[201~\t\r")
    expect(b"STATUS")
    assert (root / "executed").read_text() == command, "Run changed the command"
    send(b"q")
    deadline = time.monotonic() + 5
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, output[-2500:]
    assert termios.tcgetattr(slave) == previous
    print("PASS: discoverable Actions button, F2/Space, arrows/Enter, shortcut selection, Esc cancellation from command input and buttons, literal Japanese command, narrow resize, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
