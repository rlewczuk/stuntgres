package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"testing"
	"time"
)

// These checks concern physical identity and cleanup, not SQL session behavior.
func adapterMechanicsConn(t *testing.T, d *Driver, dsn string) *conn {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func TestSessionPhysicalIdentityMechanics(t *testing.T) {
	d := NewDriver()
	a := adapterMechanicsConn(t, d, "")
	b := adapterMechanicsConn(t, d, "")
	otherDSN := adapterMechanicsConn(t, d, " ")
	otherDriver := adapterMechanicsConn(t, NewDriver(), "")
	if a.session.Token().ID() == 0 || a.session.Token() == b.session.Token() {
		t.Fatal("physical sessions must have distinct positive identities")
	}
	if a.session.Engine() != b.session.Engine() || a.session.Engine() == otherDSN.session.Engine() || a.session.Engine() == otherDriver.session.Engine() {
		t.Fatal("engine ownership must follow exact DSN and driver identity")
	}
	token := a.session.Token()
	if err := a.ResetSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.session.Token() != token || !a.IsValid() || !a.session.IsLive() {
		t.Fatal("reuse changed physical identity or health")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh := adapterMechanicsConn(t, d, "")
	if fresh.session.Token().ID() <= token.ID() || fresh.session.Engine() != b.session.Engine() {
		t.Fatal("replacement reused identity or lost engine ownership")
	}
}

// Cancel at Reuse's second Err check using an actual cancellable context.
type adapterResetCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *adapterResetCancelContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSessionResetMechanics(t *testing.T) {
	c := adapterMechanicsConn(t, NewDriver(), "reset-mechanics")
	token := c.session.Token()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	boundary := &adapterResetCancelContext{Context: base, cancel: cancel}
	if err := c.ResetSession(boundary); err != context.Canceled {
		t.Fatalf("reset cancellation: %v", err)
	}
	if boundary.checks != 2 || base.Err() != context.Canceled {
		t.Fatalf("cancellation boundary: checks=%d err=%v", boundary.checks, base.Err())
	}
	expired, stop := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer stop()
	if err := c.ResetSession(expired); err != context.DeadlineExceeded || errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("deadline classification: %v", err)
	} else {
		var diagnostic interface{ SQLState() string }
		if errors.As(err, &diagnostic) {
			t.Fatalf("deadline fabricated SQLSTATE: %v", err)
		}
	}
	if c.session.Token() != token || !c.IsValid() || !c.session.IsLive() {
		t.Fatal("cancellation changed physical identity or health")
	}
	if err := c.ResetSession(context.Background()); err != nil {
		t.Fatal("subsequent reset", err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal("subsequent ping", err)
	}
}

func TestSessionAdapterMechanics(t *testing.T) {
	d := NewDriver()
	connector, err := d.OpenConnector("adapter-mechanics")
	if err != nil {
		t.Fatal(err)
	}
	dial, cancelDial := context.WithCancel(context.Background())
	raw, err := connector.Connect(dial)
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	defer c.Close()
	cancelDial()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal("dial context retained", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, call := range []func() error{
		func() error { return c.Ping(canceled) },
		func() error { return c.ResetSession(canceled) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation classification", err)
		}
	}
	if !c.IsValid() {
		t.Fatal("cancellation invalidated connection")
	}
	cleanupSession(cleanupCapsule{c.session})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.session.Token().ID() != 1 || c.IsValid() || c.session.IsLive() {
		t.Fatal("abandonment did not end physical session")
	}
	if err := c.ResetSession(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("ended reuse classification", err)
	}
	if _, err := connector.Connect(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled connection", err)
	}
	d.mu.Lock()
	allocated := d.lastSession
	d.lastSession = ^ids.SessionID(0)
	d.mu.Unlock()
	if allocated != 1 {
		t.Fatal("pre-canceled connection allocated an identity")
	}
	if _, err := connector.Connect(context.Background()); err == nil {
		t.Fatal("exhausted session identity wrapped")
	} else {
		var diagnostic interface{ SQLState() string }
		if errors.As(err, &diagnostic) {
			t.Fatalf("allocation failure fabricated SQLSTATE: %v", err)
		}
	}
	d.mu.Lock()
	allocated = d.lastSession
	d.mu.Unlock()
	if allocated != ^ids.SessionID(0) {
		t.Fatal("exhausted allocation changed counter")
	}
}

func TestSessionFatalNoReplayMechanics(t *testing.T) {
	c := adapterMechanicsConn(t, NewDriver(), "fatal-no-replay")
	fatal := &Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "25P04", Message: "terminating connection due to transaction timeout"}
	c.session.RequestEnd(engine.Terminated)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.session.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.terminalError(fatal); err != fatal || errors.Is(err, driver.ErrBadConn) {
		t.Fatal("accepted diagnostic permits replay", err)
	}
	if err := c.check(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("subsequent dead use remained admissible", err)
	}
	if c.IsValid() {
		t.Fatal("pool validator retained terminal physical owner")
	}
	if err := c.ResetSession(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("pool reset retained terminal physical owner", err)
	}
}
