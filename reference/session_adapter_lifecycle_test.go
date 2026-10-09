package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

type settingsResetCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *settingsResetCancelContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReferenceSessionResetCancellationBetweenChecks(t *testing.T) {
	p := newReferenceScenario(t)
	settingsNativeResetRecovery(t, p, false)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peer, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		ownerIdentity, peerIdentity := settingsPhysical(t, c), settingsPhysical(t, peer)
		run := func(e referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, e, backend.recorder, s) }
		run(c, referenceExec("CREATE TABLE sentinel(v INT)", 0))
		run(c, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
		run(c, referenceExec("SET application_name TO retained", 0))
		run(peer, referenceExec("SET application_name TO peer", 0))
		if backend.pg {
			referenceSessionCancel(t, p.ctx, c, false)
		}
		run(c, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "retained"))
		run(peer, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "peer"))
		for _, e := range []referenceExecutor{c, peer} {
			run(e, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		if settingsPhysical(t, c) != ownerIdentity || settingsPhysical(t, peer) != peerIdentity {
			t.Fatal("reset cancellation replaced physical session")
		}
	}
}

func TestReferenceSessionResetExpiredDeadline(t *testing.T) {
	p := newReferenceScenario(t)
	settingsNativeResetRecovery(t, p, true)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		identity := settingsPhysical(t, c)
		referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET application_name TO retained", 0))
		held, err := c.PrepareContext(p.ctx, "SHOW application_name")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		expired, cancel := context.WithDeadline(p.ctx, time.Unix(0, 0))
		defer cancel()
		if expired.Err() != context.DeadlineExceeded {
			t.Fatal(expired.Err())
		}
		if backend.pg {
			referenceSessionCancel(t, p.ctx, c, false)
		} else if result, execErr := c.ExecContext(expired, "SHOW application_name"); result != nil || !errors.Is(execErr, context.DeadlineExceeded) {
			t.Fatal(result, execErr)
		}
		referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "retained"))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: held, query: "SHOW application_name"}, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "retained"))
		if settingsPhysical(t, c) != identity {
			t.Fatal("deadline replaced physical session")
		}
	}
}

func TestReferenceSessionAdapterCancellationCleanupAndExhaustion(t *testing.T) {
	p := newReferenceScenario(t)
	// Direct native connector observes that the dial context is not retained.
	dial, cancelDial := context.WithCancel(p.ctx)
	raw, err := p.nativeConnector.Connect(dial)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cancelDial()
	if err = raw.(driver.Pinger).Ping(p.ctx); err != nil {
		t.Fatal("dial context retained", err)
	}
	if _, err = raw.(driver.ExecerContext).ExecContext(p.ctx, "SET application_name TO safe", nil); err != nil {
		t.Fatal(err)
	}
	canceledNative, cancelNative := context.WithCancel(p.ctx)
	cancelNative()
	if err = raw.(driver.Pinger).Ping(canceledNative); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	statement, err := raw.(driver.ConnPrepareContext).PrepareContext(canceledNative, "SHOW application_name")
	if statement != nil {
		statement.Close()
		t.Fatal("canceled adapter Prepare returned handle")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	result, err := raw.(driver.ExecerContext).ExecContext(canceledNative, "SET application_name TO lost", nil)
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(result, err)
	}
	if err = raw.(driver.SessionResetter).ResetSession(canceledNative); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !raw.(driver.Validator).IsValid() {
		t.Fatal("cancellation invalidated adapter")
	}
	if err = raw.(driver.SessionResetter).ResetSession(p.ctx); err != nil {
		t.Fatal(err)
	}
	settingsDriverValue(t, p.ctx, raw.(driver.QueryerContext), "SHOW application_name", "application_name", "TEXT", "safe")
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		identity := settingsPhysical(t, c)
		referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET application_name TO safe", 0))
		if backend.pg {
			for _, prepared := range []bool{false, true} {
				referenceSessionCancel(t, p.ctx, c, prepared)
				if err = c.PingContext(p.ctx); err != nil {
					t.Fatal("admitted cancellation damaged transport", err)
				}
			}
		} else {
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, call := range []func() error{
				func() error { return c.PingContext(canceled) },
				func() error {
					stmt, e := c.PrepareContext(canceled, "SHOW application_name")
					if stmt != nil {
						stmt.Close()
						t.Fatal("canceled Prepare returned handle")
					}
					return e
				},
				func() error {
					result, e := c.ExecContext(canceled, "SET application_name TO lost")
					if result != nil {
						t.Fatal("canceled Exec returned result")
					}
					return e
				},
			} {
				if err = call(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		}
		referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "safe"))
		if settingsPhysical(t, c) != identity {
			t.Fatal("adapter cancellation replaced physical owner")
		}
		if err = c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		if err = c.Close(); !errors.Is(err, sql.ErrConnDone) {
			t.Fatal("discarded database/sql borrower second Close", err)
		}
	}
	// Cleanup election, token saturation and no canceled allocation are nonSQL root units.
}

func TestReferenceSessionShowCommandCount(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, prepared := range []bool{false, true} {
			for _, want := range []string{"", "retained"} {
				q := "SET application_name TO DEFAULT"
				if want != "" {
					q = "SET application_name TO retained"
				}
				s := referenceExec(q, 0)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, backend.recorder, s)
				s = referenceExec("SHOW application_name", 0)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, backend.recorder, s)
				s = localReferenceSelect(backend.pg, "SHOW application_name", "application_name", want)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, backend.recorder, s)
			}
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET application_name TO DEFAULT", 0))
		}
	}
}
