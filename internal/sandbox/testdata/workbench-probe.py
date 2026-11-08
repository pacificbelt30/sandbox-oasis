"""Exercise saved environments, independent copies, results and limits in Kata."""
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

binary, project, name, archive, phase, target_index = sys.argv[1:]
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
env = os.environ | {"TERM": "xterm-256color"}
process = subprocess.Popen([binary, "ui"], cwd=project, stdin=slave, stdout=slave, stderr=slave,
    env=env, start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()




screen = Screen()
sent_at = -1


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        data = os.read(master, 65536)
        output.extend(data)
        screen.feed(data)
        del output[:-131072]


def expect(marker, timeout=45):
    deadline = time.monotonic() + timeout
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text()[-7000:])
    result = screen.text()
    output.clear()
    return result


def send(value):
    global sent_at
    sent_at = screen.received
    os.write(master, value)


def paste(value):
    send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


def reviewed_confirm():
    send(b"\t\t\r")  # scrollable text -> Cancel -> Confirm


try:
    expect(b"sort=name")
    send(b"/name:" + name.encode() + b"\r")
    expect(name.encode())
    deadline = time.monotonic() + 20
    while b"Background jobs" not in screen.text() and time.monotonic() < deadline:
        send(b"J")
        until = time.monotonic() + .1
        while time.monotonic() < until:
            pump()
    expect(b"Background jobs")
    if phase == "start":
        send(b"\t\t\r")
        expect(b"New background job")
        send(bytes([27]))
        expect(b"Background jobs")
        send(b"\t\t\r")
        expect(b"New background job")
        paste("exec python3 -u -c 'import time; print(\"JOB_READY 日本語 [red]literal\",flush=True); time.sleep(300)'")
        send(b"\t\r")
        expect(b"Start background job")
        send(b"\t\r")
        expect("JOB_READY 日本語 [red]literal".encode())
        send(bytes([27]))
        expect(b"Background jobs")
        send(bytes([27]))
        expect(b"sort=name")
    else:
        if int(target_index):
            send(b"\x1b[B" * int(target_index))
        send(b"\r")
        expect("JOB_READY 日本語 [red]literal".encode())
        send(b"\t\r")
        expect(b"Stop only job")
        send(b"\t\r")
        expect(b"Exited")
        expect(b"code")
        send(bytes([27]))
        expect(b"Background jobs")
        send(bytes([27]))
        expect(b"sort=name")
        send(b"w")
        expect(b"--- diff ---")
        send(b"\t\t\t\t\r")
        expect(b"Comparison against launch commit")
        send(b"\t\t\t\t\t\r")
        expect(b"Changed files")
        expect(b"M tracked.txt")
        send(b" \r")
        expect(b"COMMITTED_AFTER_LAUNCH")
        send(bytes([27]))
        expect(b"Changed files")
        send(b"\t\r")
        expect(b"Export selected workspace files")
        send(b"\t")
        paste(archive)
        send(b"\t\r")
        expect(b"Review selected artifacts")
        expect(b"Comparison base:")
        assert not Path(archive).exists(), "archive published before review approval"
        reviewed_confirm()
        expect(b"Saved reviewed artifacts")
        send(b"\r")
        expect(b"Export selected workspace files")
        send(bytes([27]))
        expect(b"Workspace changes")
        send(bytes([27]))
        expect(b"sort=name")
        send(b"l")
        expect(b"Host resource observations")
        expect(b"Host memory:")
        send(b"\t\r")
        expect(b"Average host CPU over")
        send(bytes([27]))
        expect(b"sort=name")
    send(b"q")
    deadline = time.monotonic() + 10
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, screen.text()[-4000:]
    assert termios.tcgetattr(slave) == previous
    print("PASS: workbench " + phase + "; terminal settings restored")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
