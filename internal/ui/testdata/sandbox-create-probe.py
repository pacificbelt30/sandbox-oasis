"""Drive sandbox creation/review/cancellation using actual terminal events."""
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

root = Path(sys.argv[2])
project = root / "project"
project.mkdir(mode=0o700)
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestSandboxDesktopPTYHelper$"], stdin=slave, stdout=slave, stderr=slave,
    env=os.environ | {"TERM": "xterm-256color", "OAISO_SANDBOX_DESKTOP_HELPER": str(root)},
    start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def read_output():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        output.extend(os.read(master, 65536))
        del output[:-65536]


def expect(marker):
    deadline = time.monotonic() + 10
    while marker not in output and time.monotonic() < deadline:
        read_output()
        if process.poll() is not None:
            break
    assert marker in output, (marker, output[-3000:])
    output.clear()


def send(value):
    os.write(master, value)


def paste(value):
    send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


try:
    expect(b"New sandbox")
    paste("ui-create-fixture")
    send(b"\t" + bytes([21]))
    paste(str(project))
    send(b"\t")
    paste("worker:test")
    send(b"\t")
    paste("printf '日本語\\n'\r\nprintf '[red]literal'")
    send(b"\x1b[Z" * 5 + b"\r")
    expect(b"false")  # AI access is disabled in the reviewed settings.
    assert not (root / "launched.json").exists(), "review launched before approval"
    send(bytes([27]))
    expect(b"New sandbox")
    assert not (root / "launched.json").exists(), "Esc approved launch"
    send(b"\r")
    expect(b"false")
    send(b"\t\r")
    expect(b"Created")
    launch = json.loads((root / "launched.json").read_text())
    assert launch["Name"] == "ui-create-fixture", launch
    assert launch["Project"] == str(project), launch
    assert launch["Image"] == "worker:test", launch
    assert launch["Command"] == "printf '日本語\\n'\nprintf '[red]literal'", launch
    assert launch["Network"] == "offline" and not launch["AI"], launch
    send(b"\r")
    expect(b"New sandbox")
    send(bytes([27]))
    deadline = time.monotonic() + 5
    while process.poll() is None and time.monotonic() < deadline:
        read_output()
    assert process.poll() == 0, output[-3000:]
    assert termios.tcgetattr(slave) == previous
    print("PASS: sandbox form, Japanese/CRLF command paste, no-AI/offline defaults, review, Esc cancellation, explicit launch, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
