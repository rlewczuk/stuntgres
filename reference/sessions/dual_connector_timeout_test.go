package reference_test

import (
	"database/sql"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// dualAwait polls cond with a 1 ms tick, bounded by fixture.Liveness.
func dualAwait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(fixture.Liveness)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		<-tick.C
	}
}

func TestReferenceDualConnectorTimeout(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	// The timeout leaves the healthy handoffs (the predialed Connect, Prepare)
	// a real margin under load; only the stalled calls reach it.
	const timeout = time.Second
	opts := dual.Options{ReferenceTimeout: timeout}

	t.Run("call", func(t *testing.T) {
		fault := newDualFault()
		d := newDualPair(t, p, opts, fault.wrap)
		for _, db := range []*sql.DB{d.Native, d.Ref} {
			dualExec(t, ctx, db, "CREATE TABLE dual_s (v int)")
			dualExec(t, ctx, db, "INSERT INTO dual_s VALUES (1)")
		}
		const update = "UPDATE dual_s SET v = v"
		release := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		fault.stall[update] = release
		fault.predial(t, ctx)
		c, err := d.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Paired before the reference is lost, used after.
		const selectV = "SELECT v FROM dual_s WHERE v = $1"
		stmt, err := c.PrepareContext(ctx, selectV)
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			n   int64
			err error
		}
		done := make(chan result, 1)
		go func() {
			res, err := c.ExecContext(ctx, update)
			var n int64
			if err == nil {
				n, err = res.RowsAffected()
			}
			done <- result{n, err}
		}()
		select {
		case r := <-done:
			if r.err != nil || r.n != 1 {
				t.Fatalf("native result %d, %v", r.n, r.err)
			}
		case <-time.After(fixture.Liveness):
			t.Fatal("the dual Exec blocked on a stalled reference")
		}
		dualExec(t, ctx, c, "SELECT 1")
		var v int64
		if err := stmt.QueryRowContext(ctx, 1).Scan(&v); err != nil || v != 1 {
			t.Fatalf("prepared caller value %d, %v", v, err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		const set2 = "UPDATE dual_s SET v = 2"
		if n := dualExec(t, ctx, tx, set2); n != 1 {
			t.Fatalf("transaction UPDATE affected %d, want 1", n)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if got := dualStrings(t, ctx, d.Native, "SELECT v FROM dual_s"); len(got) != 1 || got[0] != "2" {
			t.Errorf("native value %q after the unmirrored commit, want 2", got)
		}
		if got := dualStrings(t, ctx, d.Ref, "SELECT v FROM dual_s"); len(got) != 1 || got[0] != "1" {
			t.Errorf("reference value %q, want 1: nothing after the loss is mirrored", got)
		}
		rs := sqlRecords(d.Log.records(t))
		message := "reference did not respond within " + timeout.String() + "; connection closed"
		timed := lastQuery(t, rs, "exec", update)
		if timed.ErrorRef != "08006" || timed.ErrmsgRef != message || timed.DurationRefUS < timeout.Microseconds() {
			t.Errorf("timed-out record: error_ref %q errmsg_ref %q duration_ref_us %d", timed.ErrorRef, timed.ErrmsgRef, timed.DurationRefUS)
		}
		wantDiff(t, timed, message)
		if r := lastQuery(t, rs, "prepare", selectV); r.Unmirrored != "" || r.ResponseDiff != nil {
			t.Errorf("prepare before the loss: unmirrored %q diff %q", r.Unmirrored, r.ResponseDiff)
		}
		for _, later := range []struct{ call, query string }{
			{"exec", "SELECT 1"}, {"stmt_query", selectV}, {"stmt_close", selectV},
			{"begin", ""}, {"exec", set2}, {"commit", ""},
		} {
			r := lastQuery(t, rs, later.call, later.query)
			if r.Unmirrored != "reference connection lost" || r.ErrorRef != "08006" || r.ErrmsgRef != message || r.ResponseDiff != nil {
				t.Errorf("%s %q after the loss: unmirrored %q error_ref %q diff %q", later.call, later.query, r.Unmirrored, r.ErrorRef, r.ResponseDiff)
			}
		}
		close(release)
		released = true
		dualAwait(t, "the abandoned reference connection to close", func() bool { return fault.openConns() == 0 })
	})

	t.Run("connect", func(t *testing.T) {
		fault := newDualFault()
		d := newDualPair(t, p, opts, fault.wrap)
		release := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		fault.stallConnect = release
		// The late Connect hands out this connection although its context
		// was canceled at abandonment.
		fault.predial(t, ctx)
		if got := dualStrings(t, ctx, d.DB, "SELECT 1"); len(got) != 1 || got[0] != "1" {
			t.Fatalf("caller rows %q", got)
		}
		connect := d.Log.records(t)[0]
		failure := "reference connection failed: reference did not respond within " + timeout.String()
		if connect.Call != "connect" || connect.ErrorRef != "08001" || connect.ErrmsgRef != failure {
			t.Errorf("connect record: %+v", connect)
		}
		wantDiff(t, connect, failure)
		close(release)
		released = true
		dualAwait(t, "the late reference connection", func() bool {
			fault.mu.Lock()
			defer fault.mu.Unlock()
			return len(fault.conns) == 1
		})
		dualAwait(t, "the late reference connection to close", func() bool { return fault.openConns() == 0 })
	})
}
