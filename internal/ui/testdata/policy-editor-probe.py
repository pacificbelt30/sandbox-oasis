"""Use real terminal input/paste events to edit and save a private test policy."""
import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time
import tomllib

master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 120, 0, 0))
env = os.environ | {"TERM": "xterm-256color", "OAISO_POLICY_EDITOR_HELPER": sys.argv[2]}
process = subprocess.Popen([sys.argv[1], "-test.run", "^TestPolicyEditorPTYHelper$"], stdin=slave, stdout=slave, stderr=slave, env=env,
                           start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def expect(marker):
    deadline = time.monotonic() + 10
    while marker not in output and time.monotonic() < deadline:
        readable, _, _ = select.select([master], [], [], .05)
        if readable:
            output.extend(os.read(master, 65536))
        if process.poll() is not None:
            break
    assert marker in output, (marker, output[-3000:])
    output.clear()


def send(data):
    os.write(master, data)


try:
    expect(b"Agent policies")
    send(b"c")
    expect(b"New name:")
    send(b"team\t\r")
    expect(b"Unsaved changes")
    send(b"d")
    send(b"/description\r\r")
    expect(b"Stage")
    # The actual tcell paste event must arrive as text, including Japanese and
    # literal markup, rather than firing the editor's keyboard shortcuts.
    send(bytes([21]) + b"\x1b[200~" + "日本語 [red]literal".encode() + b"\x1b[201~\t\r")
    # Wait for the edit dialog to be removed, then open the scrollable review.
    time.sleep(.15)
    send(bytes([19]))
    expect(b"Review staged changes")
    send(b"\t\t\r")
    expect(b"Saved")
    send(bytes([27]))
    process.wait(timeout=5)
    assert process.returncode == 0
    assert termios.tcgetattr(slave) == previous
    with open(sys.argv[2], "rb") as stream:
        policy = tomllib.load(stream)
    assert policy["default"] == "team", policy
    assert policy["profiles"]["team"]["description"] == "日本語 [red]literal", policy
    assert os.stat(sys.argv[2]).st_mode & 0o777 == 0o600
    print("PASS: actual TUI keyboard navigation, bracketed Japanese paste, review, save, private file, Esc, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
