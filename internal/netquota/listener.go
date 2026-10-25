package netquota

import (
	"io"
	"math"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const burst = 32 << 10
const maxSources = 4096

// Listener accounts accepted sockets, including idle, TLS, HTTP/2 and hijacked
// connections. Close also closes hijacked sockets, which http.Server cannot do.
type Listener struct {
	net.Listener
	mu     sync.Mutex
	limits Limits
	closed bool
	peers  map[netip.Addr]*peer
	conns  map[*connection]bool
}

type peer struct {
	active int
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func Wrap(listener net.Listener, limits Limits) (*Listener, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Listener{Listener: listener, limits: limits, peers: make(map[netip.Addr]*peer), conns: make(map[*connection]bool)}, nil
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		raw, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr, err := netip.ParseAddrPort(raw.RemoteAddr().String())
		if err != nil {
			_ = raw.Close()
			continue
		}
		ip := addr.Addr().Unmap()
		l.mu.Lock()
		p := l.peers[ip]
		denied := l.closed || l.limits.MaxConnections > 0 && len(l.conns) >= l.limits.MaxConnections || p != nil && l.limits.MaxConnectionsPerSource > 0 && p.active >= l.limits.MaxConnectionsPerSource
		if p == nil && len(l.peers) >= maxSources {
			// Retain bandwidth debt across reconnects. Reclaim only a fully
			// replenished inactive bucket, never reset debt to admit a new IP.
			for key, old := range l.peers {
				if old.active == 0 && old.full(l.limits.BytesPerSecond) {
					delete(l.peers, key)
					break
				}
			}
			denied = denied || len(l.peers) >= maxSources
		}
		if denied {
			l.mu.Unlock()
			_ = raw.Close()
			continue
		}
		if p == nil {
			p = &peer{tokens: burst, last: time.Now()}
			l.peers[ip] = p
		}
		c := &connection{Conn: raw, listener: l, peer: p, done: make(chan struct{}), changed: make(chan struct{})}
		p.active++
		l.conns[c] = true
		l.mu.Unlock()
		return c, nil
	}
}

func (l *Listener) Close() error {
	err := l.Listener.Close()
	l.mu.Lock()
	l.closed = true
	connections := make([]*connection, 0, len(l.conns))
	for c := range l.conns {
		connections = append(connections, c)
	}
	l.mu.Unlock()
	for _, c := range connections {
		_ = c.Close()
	}
	return err
}

func (p *peer) refill(rate int64) {
	now := time.Now()
	p.tokens = math.Min(burst, p.tokens+now.Sub(p.last).Seconds()*float64(rate))
	p.last = now
}

func (p *peer) full(rate int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refill(rate)
	return rate == 0 || p.tokens >= burst
}

type connection struct {
	net.Conn
	readMu     sync.Mutex
	pending    []byte
	pendingErr error
	listener   *Listener
	peer       *peer
	once       sync.Once
	done       chan struct{}
	mu         sync.Mutex
	readBy     time.Time
	writeBy    time.Time
	changed    chan struct{}
}

func (c *connection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		close(c.done)
		c.listener.mu.Lock()
		delete(c.listener.conns, c)
		c.peer.active--
		c.listener.mu.Unlock()
	})
	return err
}

func (c *connection) SetDeadline(t time.Time) error {
	return c.deadline(t, true, true)
}
func (c *connection) SetReadDeadline(t time.Time) error {
	return c.deadline(t, true, false)
}
func (c *connection) SetWriteDeadline(t time.Time) error {
	return c.deadline(t, false, true)
}
func (c *connection) deadline(t time.Time, read, write bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if read {
		c.readBy = t
		err = c.Conn.SetReadDeadline(t)
	}
	if write {
		c.writeBy = t
		if e := c.Conn.SetWriteDeadline(t); e != nil {
			err = e
		}
	}
	close(c.changed)
	c.changed = make(chan struct{})
	return err
}

func (c *connection) wait(n int, read bool) error {
	rate := c.listener.limits.BytesPerSecond
	if rate == 0 || n == 0 {
		return nil
	}
	for {
		c.mu.Lock()
		deadline := c.writeBy
		if read {
			deadline = c.readBy
		}
		changed := c.changed
		c.mu.Unlock()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return os.ErrDeadlineExceeded
		}
		select {
		case <-c.done:
			return net.ErrClosed
		default:
		}
		c.peer.mu.Lock()
		c.peer.refill(rate)
		if c.peer.tokens >= float64(n) {
			c.peer.tokens -= float64(n)
			c.peer.mu.Unlock()
			return nil
		}
		delay := time.Duration(math.Ceil((float64(n) - c.peer.tokens) / float64(rate) * float64(time.Second)))
		c.peer.mu.Unlock()
		if !deadline.IsZero() && time.Until(deadline) < delay {
			delay = time.Until(deadline)
		}
		timer := time.NewTimer(delay)
		select {
		case <-c.done:
			timer.Stop()
			return net.ErrClosed
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (c *connection) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	if len(b) > burst {
		b = b[:burst]
	}
	if len(c.pending) > 0 {
		n := min(len(b), len(c.pending))
		if err := c.wait(n, true); err != nil {
			return 0, err
		}
		copy(b, c.pending[:n])
		c.pending = c.pending[n:]
		if len(c.pending) == 0 {
			err := c.pendingErr
			c.pendingErr = nil
			c.pending = nil
			return n, err
		}
		return n, nil
	}
	n, err := c.Conn.Read(b)
	if e := c.wait(n, true); e != nil {
		// net/http interrupts background reads with a temporary deadline before
		// Hijack. Never discard bytes already consumed from TCP (including TLS
		// records): retain them for the next read after that deadline is cleared.
		if n > 0 {
			c.pending = append([]byte(nil), b[:n]...)
			c.pendingErr = err
		}
		return 0, e
	}
	return n, err
}

func (c *connection) Write(b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		n := min(len(b), burst)
		if err := c.wait(n, false); err != nil {
			return total, err
		}
		written, err := c.Conn.Write(b[:n])
		total += written
		if written == 0 && err == nil {
			err = io.ErrShortWrite
		}
		if err != nil {
			return total, err
		}
		b = b[written:]
	}
	return total, nil
}
