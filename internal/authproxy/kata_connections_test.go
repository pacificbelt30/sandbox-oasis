package authproxy

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Opt-in test-only observer. Counts upstream connections without tokens, URLs
// or request contents; the acceptance harness publishes it only on loopback.
type kataFixtureConnections struct {
	active atomic.Int64
	opened atomic.Int64
}

func (c *kataFixtureConnections) track() func() {
	c.opened.Add(1)
	c.active.Add(1)
	return func() { c.active.Add(-1) }
}

func startKataConnectionObserver() (*kataFixtureConnections, func(), error) {
	c := &kataFixtureConnections{}
	if os.Getenv("CXD_KATA_NETWORK_FIXTURE") != "1" {
		return c, func() {}, nil
	}
	listener, err := net.Listen("tcp", "0.0.0.0:18083")
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{"active": c.active.Load(), "opened": c.opened.Load()})
	})}
	go func() { _ = server.Serve(listener) }()
	return c, func() { _ = server.Close() }, nil
}
