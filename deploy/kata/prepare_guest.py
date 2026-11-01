#!/usr/bin/env python3
"""Embed the PID hook into a NEW copy of the fixed Kata 4.3.0 guest image.

Requires Python 3.11+, debugfs and a statically compiled pids_hook.c binary.
No mounts, root privileges or network access are needed. Source is never opened
for writing. The manifest detects corruption; it is not a signature.
"""
import argparse
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import struct
import tempfile

HOOK_PATH = "/usr/share/codex-dock/hooks"
SOURCE_SHA256 = "1f58dcc4954a164e93fd74e34f62fdc71c60aaeaf617bb0a5a29f1c2284d7c34"


def sha(path):
    with pathlib.Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def geometry(image):
    with pathlib.Path(image).open("rb") as stream:
        mbr = stream.read(512)
    if len(mbr) != 512 or mbr[510:512] != b"\x55\xaa":
        raise ValueError("invalid guest MBR")
    entry = mbr[446:462]
    start = int.from_bytes(entry[8:12], "little")
    length = int.from_bytes(entry[12:16], "little")
    if (start, length) != (6144, 512000) or pathlib.Path(image).stat().st_size < (start+length)*512:
        raise ValueError("unsupported or truncated guest partition")
    return start*512, length*512


def run(*args):
    result = subprocess.run(args, check=True, capture_output=True, text=True)
    return result.stdout+result.stderr


def static_hook(binary):
    """The pinned image is Linux x86_64; reject dynamic/interpreted hooks."""
    with pathlib.Path(binary).open("rb") as stream:
        header = stream.read(64)
        if len(header) != 64 or header[:6] != b"\x7fELF\x02\x01" or struct.unpack_from("<H", header, 18)[0] != 62:
            raise ValueError("hook must be a Linux x86_64 ELF binary")
        offset = struct.unpack_from("<Q", header, 32)[0]
        size, count = struct.unpack_from("<HH", header, 54)
        if size < 56 or not 1 <= count <= 128 or offset+size*count > pathlib.Path(binary).stat().st_size:
            raise ValueError("invalid hook program headers")
        for i in range(count):
            stream.seek(offset+i*size)
            if struct.unpack("<I", stream.read(4))[0] == 3:  # PT_INTERP
                raise ValueError("hook must be statically linked")


def verify(manifest_path):
    manifest_path = pathlib.Path(manifest_path).resolve(strict=True)
    record = json.loads(manifest_path.read_text())
    version = record.get("schema_version")
    policy = record.get("pid_policy", "")
    if (version not in (1, 2)
            or version == 1 and policy != ""
            or version == 2 and policy != "oci-annotation-v1"
            or record["pids_limit"] != 512
            or record["guest_hook_path"] != HOOK_PATH
            or record["source_image_sha256"] != SOURCE_SHA256
            or set(record["files"]) != {"guest.image", "pids-hook", "pids_hook.c"}):
        raise ValueError("unsupported guest manifest")
    for name, checksum in record["files"].items():
        path = manifest_path.parent/name
        if path.is_symlink() or path.resolve().parent != manifest_path.parent or path.stat().st_mode & 0o022 or sha(path) != checksum:
            raise ValueError("guest asset checksum/permissions mismatch: "+name)
    return record


def build(source, binary, output):
    source = pathlib.Path(source).resolve(strict=True)
    binary = pathlib.Path(binary).resolve(strict=True)
    output = pathlib.Path(output).absolute()
    # debugfs's own command language has no general shell-style escaping.
    if any(c.isspace() or c in '\"\\' for c in str(output)):
        raise ValueError("output path cannot contain whitespace, quotes or backslashes")
    if sha(source) != SOURCE_SHA256:
        raise ValueError("unsupported source image; verify the fixed Kata release before changing its pin")
    offset, length = geometry(source)
    static_hook(binary)
    output.mkdir(mode=0o755, exist_ok=False)
    image = output/"guest.image"
    shutil.copyfile(source, image)
    shutil.copyfile(binary, output/"pids-hook")
    shutil.copyfile(pathlib.Path(__file__).with_name("pids_hook.c"), output/"pids_hook.c")
    with tempfile.TemporaryDirectory(prefix="codex-dock-guest-", dir=output) as temp:
        partition = pathlib.Path(temp)/"partition.ext4"
        with source.open("rb") as src, partition.open("wb") as dst:
            src.seek(offset)
            remaining = length
            while remaining:
                data = src.read(min(1024*1024, remaining))
                if not data:
                    raise ValueError("truncated source image")
                dst.write(data)
                remaining -= len(data)
        for directory in ["/usr/share/codex-dock", HOOK_PATH, HOOK_PATH+"/prestart"]:
            run("debugfs", "-w", "-R", "mkdir "+directory, str(partition))
        hook = HOOK_PATH+"/prestart/10-pids-limit"
        run("debugfs", "-w", "-R", f"write {output/'pids-hook'} {hook}", str(partition))
        run("debugfs", "-w", "-R", f"set_inode_field {hook} mode 0100755", str(partition))
        stat = run("debugfs", "-R", f"stat {hook}", str(partition))
        if not re.search(r"Type:\s+regular\s+Mode:\s+0?755\b", stat):
            raise ValueError("embedded hook is not executable")
        extracted = pathlib.Path(temp)/"extracted-hook"
        run("debugfs", "-R", f"dump {hook} {extracted}", str(partition))
        # debugfs can return zero even after an internal command error.
        if not extracted.is_file() or sha(extracted) != sha(binary):
            raise ValueError("embedded hook does not match compiled input")
        with image.open("r+b") as dst, partition.open("rb") as src:
            dst.seek(offset)
            shutil.copyfileobj(src, dst)
    files = {}
    for name in ["guest.image", "pids-hook", "pids_hook.c"]:
        path = output/name
        path.chmod(0o755 if name == "pids-hook" else 0o644)
        files[name] = sha(path)
    if sha(source) != SOURCE_SHA256:
        raise ValueError("source image changed during packaging")
    manifest = output/"assets.lock.json"
    manifest.write_text(json.dumps({"schema_version": 2, "pid_policy": "oci-annotation-v1", "pids_limit": 512, "guest_hook_path": HOOK_PATH,
                                    "source_image_sha256": SOURCE_SHA256, "files": files}, indent=2)+"\n")
    manifest.chmod(0o644)
    verify(manifest)
    print(manifest)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    package = sub.add_parser("build")
    package.add_argument("--source-image", required=True)
    package.add_argument("--hook-binary", required=True)
    package.add_argument("--output", required=True)
    check = sub.add_parser("verify")
    check.add_argument("manifest")
    args = parser.parse_args()
    if args.command == "build":
        build(args.source_image, args.hook_binary, args.output)
    else:
        verify(args.manifest)
        print("Guest assets verified")


if __name__ == "__main__":
    main()
