"""Exercise actual task-board form navigation, bracketed paste and cancellation."""
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
subprocess.run(["git", "init", "-b", "main", str(project)], check=True, capture_output=True)
subprocess.run(["git", "-C", str(project), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "base"], check=True, capture_output=True)
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestTaskBoardPTYHelper$"], stdin=slave, stdout=slave, stderr=slave,
    env=os.environ | {"TERM": "xterm-256color", "OAISO_TASK_BOARD_HELPER": str(root)},
    start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()

def expect(marker):
    deadline = time.monotonic() + 10
    while marker not in output and time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], .05)
        if ready:
            output.extend(os.read(master, 65536))
        if process.poll() is not None:
            break
    assert marker in output, (marker, output[-4000:])
    output.clear()

def send(data):
    os.write(master, data)

def paste(text):
    send(b"\x1b[200~" + text.encode() + b"\x1b[201~")

try:
    expect(b"No tasks")
    send(b"n")
    expect(b"Instructions")
    paste("日本語 [red]task")
    send(b"\t" + bytes([21]))
    paste(str(project))
    send(b"\t\t")
    paste("first line\r\n日本語 [blue]second line")
    send(b"\t\t\t\t\t\r")
    deadline = time.monotonic() + 10
    path = root / "private" / "board.json"
    while not path.exists() and time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], .05)
        if ready:
            output.extend(os.read(master, 65536))
    assert path.exists(), output[-4000:]
    task = json.loads(path.read_text())["tasks"][0]
    assert task["spec"]["title"] == "日本語 [red]task", task
    assert task["spec"]["prompt"] == "first line\n日本語 [blue]second line", task
    assert task["state"] == "queued", task
    assert path.stat().st_mode & 0o777 == 0o600
    # Storage is written before the UI callback renders the saved task.
    # Wait for that task's visible ID rather than guessing a render delay.
    expect(task["id"].encode())
    send(b"n")
    expect(b"Instructions")
    paste("__CANCEL_ONLY__")
    # Consume every older frame before looking for the board's return marker.
    expect(b"__CANCEL_ONLY__")
    send(bytes([27]))
    # Draws may exceed the PTY buffer on CI. Read until cancellation actually
    # returns to the board before sending another navigation key.
    expect(b"Q/Esc")
    send(b"q")
    # Keep draining output while the screen is restored and the helper exits;
    # waiting without reading can block the child's terminal writer forever.
    deadline = time.monotonic() + 5
    while process.poll() is None and time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], .05)
        if ready:
            output.extend(os.read(master, 65536))
    assert process.poll() is not None, ("terminal helper did not exit", output[-4000:])
    assert process.returncode == 0
    assert len(json.loads(path.read_text())["tasks"]) == 1
    assert termios.tcgetattr(slave) == previous
    print("PASS: real task board, Japanese/multiline bracketed paste, Tab navigation, private queue, Esc cancellation, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
