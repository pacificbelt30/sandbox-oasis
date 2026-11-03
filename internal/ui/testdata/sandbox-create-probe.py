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

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "sandbox" / "testdata"))
from terminal_screen import Screen

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
screen = Screen(48, 150)
sent_at = -1


def read_output():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        data = os.read(master, 65536)
        output.extend(data)
        screen.feed(data)
        del output[:-65536]


def expect(marker):
    deadline = time.monotonic() + 10
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        read_output()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text()[-7000:])
    output.clear()


def send(value):
    global sent_at
    sent_at = screen.received
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
    send(bytes([19]))  # Ctrl-S: save settings without launching.
    expect(b"Save environment")
    paste("Python検証")
    send(b"\x1b[Z\x1b[Z\r")  # first field -> Cancel -> Review save
    expect(b"new environment")
    saved = root / "private" / "environments.json"
    assert not saved.exists(), "environment saved before approval"
    send(b"\t\t\r")
    expect(b"Saved environment")
    settings = json.loads(saved.read_text())["environments"]["Python検証"]
    assert settings["Command"] == "printf '日本語\\n'\nprintf '[red]literal'", settings
    assert "AI" not in settings and "Mounts" not in settings and "Project" not in settings
    assert saved.stat().st_mode & 0o777 == 0o600
    send(b"\r")
    expect(b"New sandbox")
    send(bytes([15]))  # Ctrl-O: loading also requires review.
    expect("Python検証".encode())
    send(b"\r")
    expect(b"Load environment")
    send(b"\t\t\r")
    expect(b"New sandbox")
    assert not (root / "launched.json").exists(), "save/load launched the VM"
    send(bytes([18]))  # Ctrl-R: review without depending on form length.
    expect(b"false")  # AI access is disabled in the reviewed settings.
    assert not (root / "launched.json").exists(), "review launched before approval"
    send(bytes([27]))
    expect(b"New sandbox")
    assert not (root / "launched.json").exists(), "Esc approved launch"
    send(bytes([18]))
    expect(b"false")
    send(b"\t\t\r")
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
    print("PASS: sandbox form; Japanese environment name and CRLF command paste; private save/load reviews without launch; no-AI/offline defaults; launch review, Esc cancellation, explicit launch and terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
