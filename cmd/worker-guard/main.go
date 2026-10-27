// worker-guard is the static, operator-installed PID 1 bootstrap for Kata.
// No image entrypoint or user command runs until the host has verified the VM.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const socket = "/run/codex-dock-control/release.sock"

func die(err error) { fmt.Fprintln(os.Stderr, "worker-guard:", err); os.Exit(1) }
func main() {
	if len(os.Args) < 2 {
		die(fmt.Errorf("missing operation"))
	}
	switch os.Args[1] {
	case "capabilities":
		if err := json.NewEncoder(os.Stdout).Encode([]string{"temporary-storage-probe-v1", "worker-mtls-v1", "process-nofile-v1", "agent-state-probe-v1"}); err != nil {
			die(err)
		}
	case "transport-install":
		if err := installTransport(); err != nil {
			die(err)
		}
	case "transport":
		if err := runTransport(); err != nil {
			die(err)
		}
	case "wait":
		if len(os.Args) < 4 || os.Getpid() != 1 {
			die(fmt.Errorf("expected PID 1, allocation nonce and command"))
		}
		if err := prepareAgentState(); err != nil {
			die(err)
		}
		if err := os.MkdirAll("/run/codex-dock-control", 01777); err != nil {
			die(err)
		}
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			die(err)
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		if err != nil {
			die(err)
		}
		if err = listener.SetDeadline(time.Now().Add(120 * time.Second)); err != nil {
			die(err)
		}
		conn, err := listener.AcceptUnix()
		if err != nil {
			die(err)
		}
		if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			die(err)
		}
		var nonce string
		if err = json.NewDecoder(conn).Decode(&nonce); err != nil || nonce != os.Args[2] {
			die(fmt.Errorf("invalid release nonce"))
		}
		if _, err = conn.Write([]byte("ready\n")); err != nil {
			die(err)
		}
		_ = conn.Close()
		_ = listener.Close()
		_ = os.Remove(socket)
		if os.Getenv("CODEX_DOCK_WORKER_MTLS") == "1" {
			if err = startTransport(); err != nil {
				die(err)
			}
		}
		command, err := exec.LookPath(os.Args[3])
		if err != nil {
			die(err)
		}
		var env []string
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "CODEX_DOCK_VERIFIED_KATA=") {
				env = append(env, entry)
			}
		}
		env = append(env, "CODEX_DOCK_VERIFIED_KATA=1")
		if err = syscall.Exec(command, os.Args[3:], env); err != nil {
			die(err)
		}
	case "probe":
		out := map[string]string{}
		for _, name := range []string{"status", "cmdline"} {
			data, err := os.ReadFile("/proc/1/" + name)
			if err != nil {
				die(err)
			}
			out[name] = string(data)
		}
		for name, path := range map[string]string{"boot_id": "/proc/sys/kernel/random/boot_id", "pids": "/sys/fs/cgroup/pids.max", "memory": "/sys/fs/cgroup/memory.max", "cpu": "/sys/fs/cgroup/cpu.max"} {
			data, err := os.ReadFile(path)
			if err != nil {
				die(err)
			}
			out[name] = strings.TrimSpace(string(data))
		}
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
			die(err)
		}
		out["nofile"] = fmt.Sprintf("%d %d", limit.Cur, limit.Max)
		initLimits, err := os.ReadFile("/proc/1/limits")
		if err != nil {
			die(err)
		}
		for _, line := range strings.Split(string(initLimits), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 6 && strings.Join(fields[:3], " ") == "Max open files" {
				out["init_nofile"] = fields[3] + " " + fields[4]
			}
		}
		if len(os.Args) > 2 {
			observations, err := temporaryObservations(os.Args[2:])
			if err != nil {
				die(err)
			}
			data, err := json.Marshal(observations)
			if err != nil {
				die(err)
			}
			out["temporary_storage"] = string(data)
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			die(err)
		}
	case "release":
		if len(os.Args) != 3 {
			die(fmt.Errorf("missing nonce"))
		}
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "unix", socket)
		if err != nil {
			die(err)
		}
		defer func() { _ = conn.Close() }()
		if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			die(err)
		}
		if err = json.NewEncoder(conn).Encode(os.Args[2]); err != nil {
			die(err)
		}
		data := make([]byte, 32)
		n, err := conn.Read(data)
		if err != nil || string(data[:n]) != "ready\n" {
			die(fmt.Errorf("worker was not released"))
		}
	default:
		die(fmt.Errorf("unknown operation"))
	}
}
