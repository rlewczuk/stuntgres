package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native replacement has no PostgreSQL SQL equivalent. Both callers keep this
// phase inside their otherwise shared dual-mode public scenario.
func prefetchNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement) {
	t.Helper()
	statsExec(t, p, b, c, "SET application_name='prefetch controls'", 0)
	var pid int64
	var principal, mode string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),current_user,current_setting('stats_fetch_consistency')").Scan(&pid, &principal, &mode); err != nil {
		t.Fatal(err)
	}
	before := prefetchGet(t, p, b, held)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_stat_recovery_prefetch") || strings.Contains(dump.String(), "stats_reset") {
		t.Fatal("Dump serialized monitoring history", dump.String())
	}
	prefetchSame(t, before, prefetchGet(t, p, b, held))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("not valid SQL")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	prefetchSame(t, before, prefetchGet(t, p, b, held))
	prefetchSentinel(t, p, b, c)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, control := range []func() error{
		func() error { return stuntgres.Reset(ctx, p.Native) },
		func() error { return stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())) },
	} {
		if err := control(); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled native control", err)
		}
		prefetchSame(t, before, prefetchGet(t, p, b, held))
		prefetchSentinel(t, p, b, c)
	}
	// Leave actual database/sql Rows unconsumed across each accepted replacement.
	// The held statement must instead execute against the newly installed owner.
	for _, restore := range []bool{false, true} {
		old := prefetchGet(t, p, b, held)
		retained, err := held.QueryContext(p.Ctx, prefetchSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		lower := prefetchClock(t, p, c)
		if restore {
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
		} else {
			err = stuntgres.Reset(p.Ctx, p.Native)
		}
		if err != nil {
			t.Fatal(err)
		}
		upper := prefetchClock(t, p, c)
		prefetchSame(t, old, prefetchRead(t, retained, false))
		fresh := prefetchGet(t, p, b, held)
		prefetchEvent(t, old, fresh, lower, upper)
		prefetchSame(t, fresh, prefetchGet(t, p, b, c))
		var gotPID int64
		var gotPrincipal, gotMode, application string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),current_user,current_setting('stats_fetch_consistency'),current_setting('application_name')").Scan(&gotPID, &gotPrincipal, &gotMode, &application); err != nil {
			t.Fatal(err)
		}
		if gotPID != pid || gotPrincipal != principal || gotMode != mode || application != "prefetch controls" {
			t.Fatal("native control replaced live session authority/settings", gotPID, gotPrincipal, gotMode, application)
		}
		if restore {
			prefetchSentinel(t, p, b, c)
		} else {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='prefetch_sentinel' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	}
}
