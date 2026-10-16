package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Exact ResetSession admission operates on the actual public native adapter, not
// a middleware interface proxy. Recorded SQL companions stay on p.backends().
func settingsNativeResetRecovery(t *testing.T, p *fixture.Pair, expired bool) {
	t.Helper()
	a, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(); err != nil {
			t.Error("owner driver Close", err)
		}
	}()
	b, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Error("peer driver Close", err)
		}
	}()
	if a == b {
		t.Fatal("reset owner and peer share physical adapter")
	}
	for _, setup := range []struct {
		conn driver.Conn
		sql  []string
	}{
		{a, []string{"SET application_name TO retained", "CREATE TABLE raw_reset_sentinel(v INT)", "INSERT INTO raw_reset_sentinel VALUES(7)"}},
		{b, []string{"SET application_name TO peer"}},
	} {
		for _, query := range setup.sql {
			if _, err := setup.conn.(driver.ExecerContext).ExecContext(p.Ctx, query, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	reset := a.(driver.SessionResetter)
	var failure error
	if expired {
		ctx, cancel := context.WithDeadline(p.Ctx, time.Unix(0, 0))
		defer cancel()
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatal(ctx.Err())
		}
		failure = reset.ResetSession(ctx)
		if failure != context.DeadlineExceeded {
			t.Fatal(failure)
		}
	} else {
		base, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		boundary := &settingsResetCancelContext{Context: base, cancel: cancel}
		failure = reset.ResetSession(boundary)
		if failure != context.Canceled || boundary.checks != 2 || base.Err() != context.Canceled {
			t.Fatal("second-check cancellation", boundary.checks, failure)
		}
	}
	var state interface{ SQLState() string }
	if errors.Is(failure, driver.ErrBadConn) || errors.As(failure, &state) || !a.(driver.Validator).IsValid() {
		t.Fatal("reset classification/health", failure)
	}
	if err := reset.ResetSession(p.Ctx); err != nil {
		t.Fatal("subsequent reset", err)
	}
	if err := a.(driver.Pinger).Ping(p.Ctx); err != nil {
		t.Fatal("subsequent ping", err)
	}
	for _, c := range []driver.Conn{a, b} {
		if !c.(driver.Validator).IsValid() {
			t.Fatal("reset recovery invalidated adapter")
		}
		value := "retained"
		if c == b {
			value = "peer"
		}
		settingsDriverValue(t, p.Ctx, c.(driver.QueryerContext), "SHOW application_name", "application_name", "TEXT", value)
		settingsDriverValue(t, p.Ctx, c.(driver.QueryerContext), "SELECT v FROM raw_reset_sentinel", "v", "INT4", int64(7))
	}
}
