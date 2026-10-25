#!/bin/sh
# Root-only setup of a dedicated subtree. Never move/kill processes or
# change another service's resource limits. Run after verifying the node layout.
set -eu
test "$(id -u)" = 0
group=/sys/fs/cgroup/codex-dock
test "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs
for controller in cpu memory pids; do
    grep -qw "$controller" /sys/fs/cgroup/cgroup.subtree_control
done
mkdir -p "$group"
test ! -L "$group"
test -z "$(cat "$group/cgroup.procs")"
# Existing worker envelopes survive node-service restarts. Only the empty
# parent is configured; no child or its occupants are changed.
test "$(stat -c %u "$group")" = 0
controllers='+cpu +memory +pids'
# I/O is optional for existing profiles. Never enable controllers in another
# service's subtree; the operator must delegate io from the parent first.
if grep -qw io /sys/fs/cgroup/cgroup.subtree_control; then
    controllers="$controllers +io"
fi
printf '%s\n' "$controllers" > "$group/cgroup.subtree_control"
chmod 0755 "$group"
chown 0:0 "$group" "$group/cgroup.procs" "$group/cgroup.threads" "$group/cgroup.subtree_control"
