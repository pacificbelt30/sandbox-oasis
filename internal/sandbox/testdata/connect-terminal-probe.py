"""Exercise the public CLI using an actual host PTY and the guest PTY.

Synthetic input only. The caller owns worker lifecycle and captures failures.
"""
import base64
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time

keys = bytes.fromhex("1b5b41031a097f1b5b337e1b5b48")
paste = bytes.fromhex("1b5b3230307e") + "日本語の貼り付け\n装飾_┌─┐_✓".encode() + bytes.fromhex("1b5b3230317e")
payload = keys + paste
guest = '''import os, termios, tty, time
previous = termios.tcgetattr(0)
try:
    tty.setraw(0)
    size = os.get_terminal_size(0)
    os.write(1, ("INITIAL_%dx%d" % (size.columns, size.lines)).encode() + bytes([10]))
    os.write(1, b"PROBE_READY" + bytes([10]))
    first = os.read(0, 1)
    assert first == bytes([27]), first
    os.write(1, b"ESC_WITHOUT_NEWLINE" + bytes([10]))
    data = b""
    while len(data) < PAYLOAD_LENGTH:
        chunk = os.read(0, PAYLOAD_LENGTH - len(data))
        if not chunk:
            raise RuntimeError("unexpected stdin EOF")
        data += chunk
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        size = os.get_terminal_size(0)
        if (size.columns, size.lines) == (101, 37):
            break
        time.sleep(.02)
finally:
    termios.tcsetattr(0, termios.TCSADRAIN, previous)
os.write(1, bytes.fromhex("1b5b313b33383b353b3139366d") + b"ANSI_RED_BOLD" + bytes.fromhex("1b5b306d0a"))
os.write(1, "UNICODE_装飾_┌─┐_✓".encode() + bytes([10]))
os.write(1, ("KEYS_" + (first + data).hex() + " SIZE_%dx%d TERM_%s" % (size.columns, size.lines, os.environ.get("TERM", ""))).encode() + bytes([10]))
'''.replace("PAYLOAD_LENGTH", str(len(payload)))
encoded = base64.b64encode(guest.encode()).decode()
command = "python3 -c \"import base64;exec(base64.b64decode('%s'))\"" % encoded
primary = sys.argv[2] == "--primary"
args = ([sys.argv[1], "run", *sys.argv[3:], "--keep", "--no-internet", "--exec", command]
        if primary else [sys.argv[1], "connect", "--shell", "sh", sys.argv[2]])
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 33, 91, 0, 0))
process = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave, close_fds=True,
                           start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def collect_until(marker, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline and marker not in output:
        readable, _, _ = select.select([master], [], [], .1)
        if readable:
            try:
                output.extend(os.read(master, 65536))
            except OSError:
                break
        if process.poll() is not None:
            break
    if marker not in output:
        raise RuntimeError("missing terminal marker: " + repr(marker))


try:
    if not primary:
        # A shell command may arrive before the client enters raw mode, but
        # special bytes are sent only after the guest handshake below.
        os.write(master, (command + "\n").encode())
    collect_until(b"PROBE_READY")
    os.write(master, bytes([27]))
    collect_until(b"ESC_WITHOUT_NEWLINE", timeout=3)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 37, 101, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    os.write(master, payload)
    marker = b"KEYS_" + (bytes([27]) + payload).hex().encode()
    collect_until(marker)
    collect_until(b"TERM_xterm-256color")
    if not primary:
        os.write(master, b"exit" + bytes([13]))
    process.wait(timeout=10)
    assert process.returncode == 0, process.returncode
    assert termios.tcgetattr(slave) == previous, "host terminal mode not restored"
    # The next UI/host reader must own stdin after the session exits.
    os.write(master, b"RESUMED_INPUT\n")
    readable, _, _ = select.select([slave], [], [], 2)
    assert readable and os.read(slave, 64) == b"RESUMED_INPUT\n", "stdin reader survived session exit"
    ansi = bytes.fromhex("1b5b313b33383b353b3139366d") + b"ANSI_RED_BOLD" + bytes.fromhex("1b5b306d")
    for required in [b"INITIAL_91x33", b"SIZE_101x37", ansi, "UNICODE_装飾_┌─┐_✓".encode()]:
        assert required in output, repr(required)
    print("PASS: " + ("primary" if primary else "connect") + " Esc without newline, keys, UTF-8 bracketed paste, live resize, ANSI, terminal restore, resumed stdin")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
    sys.stdout.buffer.write(output)
