package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// pingDriver's connections answer Ping with ping.
type pingDriver struct{ ping func(context.Context) error }

func (d pingDriver) Open(string) (driver.Conn, error) { return pingConn(d), nil }

type pingConn pingDriver

func (c pingConn) Ping(ctx context.Context) error         { return c.ping(ctx) }
func (pingConn) Prepare(string) (driver.Stmt, error)      { return nil, errors.New("unused") }
func (pingConn) Close() error                             { return nil }
func (pingConn) Begin() (driver.Tx, error)                { return nil, errors.New("unused") }
func (pingConn) ResetSession(context.Context) error       { return nil }
func (pingConn) IsValid() bool                            { return true }
func (pingConn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (pingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("unused")
}

func probeConn(t *testing.T, ping func(context.Context) error) *sql.Conn {
	t.Helper()
	db := sql.OpenDB(connector{pingDriver{ping}})
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

type connector struct{ d pingDriver }

func (c connector) Connect(context.Context) (driver.Conn, error) { return pingConn(c.d), nil }
func (c connector) Driver() driver.Driver                        { return c.d }

// A ping that failed has answered: probe reports no late ping, so the connection is released at
// once (WithLock would otherwise wait forever on an empty channel). A ping that outlives its
// timeout is reported late, and its channel gets the ping's one answer.
func TestProbeReportsOnlyAPingStillRunning(t *testing.T) {
	owned, cancel := context.WithCancelCause(context.Background())
	if late := probe(owned, probeConn(t, func(context.Context) error { return errors.New("network") }), cancel); late != nil {
		t.Error("a failed ping was reported as still running")
	}
	if !errors.Is(context.Cause(owned), ErrOwnershipLost) {
		t.Errorf("cause %v, want ErrOwnershipLost", context.Cause(owned))
	}

	release := make(chan struct{})
	owned, cancel = context.WithCancelCause(context.Background())
	late := probe(owned, probeConn(t, func(context.Context) error { <-release; return errors.New("late") }), cancel)
	if late == nil {
		t.Fatal("a ping past its timeout was not reported")
	}
	close(release)
	select {
	case <-late:
	case <-time.After(5 * time.Second):
		t.Fatal("the late ping never answered")
	}
}
