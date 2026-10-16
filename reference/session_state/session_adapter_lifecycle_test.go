package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
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
	p := fixture.NewScenario(t)
	settingsNativeResetRecovery(t, p, false)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peer, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		ownerIdentity, peerIdentity := fixture.SettingsPhysical(t, c), fixture.SettingsPhysical(t, peer)
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, backend.Recorder, s) }
		run(c, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(c, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
		run(c, fixture.Exec("SET application_name TO retained", 0))
		run(peer, fixture.Exec("SET application_name TO peer", 0))
		if backend.PG {
			fixture.SessionCancel(t, p.Ctx, c, false)
		}
		run(c, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "retained"))
		run(peer, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "peer"))
		for _, e := range []fixture.Executor{c, peer} {
			run(e, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		if fixture.SettingsPhysical(t, c) != ownerIdentity || fixture.SettingsPhysical(t, peer) != peerIdentity {
			t.Fatal("reset cancellation replaced physical session")
		}
	}
}

func TestReferenceSessionResetExpiredDeadline(t *testing.T) {
	p := fixture.NewScenario(t)
	settingsNativeResetRecovery(t, p, true)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		identity := fixture.SettingsPhysical(t, c)
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET application_name TO retained", 0))
		held, err := c.PrepareContext(p.Ctx, "SHOW application_name")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		expired, cancel := context.WithDeadline(p.Ctx, time.Unix(0, 0))
		defer cancel()
		if expired.Err() != context.DeadlineExceeded {
			t.Fatal(expired.Err())
		}
		if backend.PG {
			fixture.SessionCancel(t, p.Ctx, c, false)
		} else if result, execErr := c.ExecContext(expired, "SHOW application_name"); result != nil || !errors.Is(execErr, context.DeadlineExceeded) {
			t.Fatal(result, execErr)
		}
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "retained"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: "SHOW application_name"}, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "retained"))
		if fixture.SettingsPhysical(t, c) != identity {
			t.Fatal("deadline replaced physical session")
		}
	}
}

func TestReferenceSessionAdapterCancellationCleanupAndExhaustion(t *testing.T) {
	p := fixture.NewScenario(t)
	// Direct native connector observes that the dial context is not retained.
	dial, cancelDial := context.WithCancel(p.Ctx)
	raw, err := p.NativeConnector.Connect(dial)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cancelDial()
	if err = raw.(driver.Pinger).Ping(p.Ctx); err != nil {
		t.Fatal("dial context retained", err)
	}
	if _, err = raw.(driver.ExecerContext).ExecContext(p.Ctx, "SET application_name TO safe", nil); err != nil {
		t.Fatal(err)
	}
	canceledNative, cancelNative := context.WithCancel(p.Ctx)
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
	if err = raw.(driver.SessionResetter).ResetSession(p.Ctx); err != nil {
		t.Fatal(err)
	}
	settingsDriverValue(t, p.Ctx, raw.(driver.QueryerContext), "SHOW application_name", "application_name", "TEXT", "safe")
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		identity := fixture.SettingsPhysical(t, c)
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET application_name TO safe", 0))
		if backend.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared)
				if err = c.PingContext(p.Ctx); err != nil {
					t.Fatal("admitted cancellation damaged transport", err)
				}
			}
		} else {
			canceled, cancel := context.WithCancel(p.Ctx)
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
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "safe"))
		if fixture.SettingsPhysical(t, c) != identity {
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
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
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
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
				s = fixture.Exec("SHOW application_name", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
				s = localReferenceSelect(backend.PG, "SHOW application_name", "application_name", want)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET application_name TO DEFAULT", 0))
		}
	}
}
