package testdb

import (
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
)

// Relay is a loopback TCP relay to a database that a test can freeze: a frozen relay keeps
// every connection open and forwards nothing, as a paused VM or a one-sided partition does.
type Relay struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	cond   *sync.Cond
	frozen bool
	conns  []net.Conn
}

// NewRelay relays loopback connections to target (host:port) until the test ends.
func NewRelay(t *testing.T, target string) *Relay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &Relay{ln: ln, target: target}
	r.cond = sync.NewCond(&r.mu)
	go r.serve()
	t.Cleanup(r.Close)
	return r
}

// Addr is the relay's host:port.
func (r *Relay) Addr() string { return r.ln.Addr().String() }

// Freeze stops forwarding on every connection, open or new.
func (r *Relay) Freeze() { r.mu.Lock(); r.frozen = true; r.mu.Unlock() }

// Thaw forwards again.
func (r *Relay) Thaw() { r.mu.Lock(); r.frozen = false; r.mu.Unlock(); r.cond.Broadcast() }

// Close stops the relay and drops every connection.
func (r *Relay) Close() {
	_ = r.ln.Close()
	r.mu.Lock()
	for _, c := range r.conns {
		_ = c.Close()
	}
	r.frozen = false
	r.mu.Unlock()
	r.cond.Broadcast()
}

func (r *Relay) serve() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		go r.pipe(c)
	}
}

func (r *Relay) pipe(c net.Conn) {
	r.wait()
	up, err := net.Dial("tcp", r.target)
	if err != nil {
		_ = c.Close()
		return
	}
	r.mu.Lock()
	r.conns = append(r.conns, c, up)
	r.mu.Unlock()
	go r.copy(up, c)
	r.copy(c, up)
}

func (r *Relay) wait() {
	r.mu.Lock()
	for r.frozen {
		r.cond.Wait()
	}
	r.mu.Unlock()
}

func (r *Relay) copy(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.wait()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return // EOF or a closed connection
		}
	}
}

// Via is cfg's PostgreSQL DSN pointed at the relay instead of the database.
func Via(t *testing.T, cfg config.DatabaseConfig, r *Relay) config.DatabaseConfig {
	t.Helper()
	u, err := url.Parse(cfg.DSN)
	if err != nil || !strings.HasPrefix(cfg.DSN, "postgres") {
		t.Fatalf("testdb: not a PostgreSQL URL: %v", err)
	}
	u.Host = r.Addr()
	cfg.DSN = u.String()
	return cfg
}

// PostgresTarget is the host:port of KY_TEST_POSTGRES_DSN, for a relay.
func PostgresTarget(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}
