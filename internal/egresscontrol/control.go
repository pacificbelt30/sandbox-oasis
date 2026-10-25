// Package egresscontrol defines a credential-free, local Unix-socket contract.
package egresscontrol

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

const Directory = "/var/lib/oaiso-egress"
const Socket = "control.sock"

type State struct {
	Generation uint64              `json:"generation"`
	PolicyID   string              `json:"policy_id"`
	Ready      bool                `json:"ready"`
	Rules      networkpolicy.Rules `json:"rules"`
}
type Update struct {
	ExpectedGeneration uint64              `json:"expected_generation"`
	Rules              networkpolicy.Rules `json:"rules"`
}

func ID(r networkpolicy.Rules) string {
	for _, v := range []*[]string{&r.AllowDomains, &r.DenyDomains, &r.AllowCIDRs, &r.DenyCIDRs} {
		*v = append([]string(nil), *v...)
		slices.Sort(*v)
	}
	for _, v := range []*[]int{&r.AllowPorts, &r.DenyPorts} {
		*v = append([]int(nil), *v...)
		slices.Sort(*v)
	}
	data, _ := json.Marshal(struct {
		Version string
		Rules   networkpolicy.Rules
	}{"policy-v1", r})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func Call(ctx context.Context, directory string, update *Update) (State, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", directory+"/"+Socket)
	}}
	defer transport.CloseIdleConnections()
	method := "GET"
	var body io.Reader
	if update != nil {
		method = "POST"
		data, err := json.Marshal(update)
		if err != nil {
			return State{}, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://local/policy", body)
	if err != nil {
		return State{}, err
	}
	resp, err := (&http.Client{Transport: transport, Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return State{}, fmt.Errorf("policy control unavailable; inspect before retrying")
	}
	defer func() { _ = resp.Body.Close() }()
	var out State
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return out, fmt.Errorf("invalid policy control response")
	}
	if resp.StatusCode != 200 || update != nil && !out.Ready {
		return out, fmt.Errorf("policy not confirmed (HTTP %d); inspect before retrying", resp.StatusCode)
	}
	return out, nil
}

// ReadUpdate consumes exactly one bounded newline-delimited management frame.
// A runc exec may retain its FIFO after transport half-close, so helpers must
// not wait for process stdin EOF before issuing the control operation.
func ReadUpdate(in io.Reader) (Update, error) {
	line, err := bufio.NewReader(io.LimitReader(in, 64<<10+1)).ReadBytes('\n')
	if err != nil {
		return Update{}, fmt.Errorf("policy update requires one bounded JSON line")
	}
	var update Update
	if err := strictjson.Decode(bytes.NewReader(line[:len(line)-1]), 64<<10, &update); err != nil {
		return Update{}, err
	}
	return update, nil
}
