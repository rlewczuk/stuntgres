package dual

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDualReferenceTimeout(t *testing.T) {
	ctx := context.Background()
	// The timeout leaves the healthy Connect, Begin and Prepare a real margin
	// under load; only the stalled Exec reaches it.
	const timeout = time.Second
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	var refCalls atomic.Int32
	ref := &execConn{fakeConn: &fakeConn{}, exec: func(context.Context, string) (driver.Result, error) {
		refCalls.Add(1)
		<-release // ignores its context, like a stuck server
		return driver.RowsAffected(1), nil
	}}
	native := &execConn{fakeConn: &fakeConn{}, exec: func(context.Context, string) (driver.Result, error) {
		return driver.RowsAffected(1), nil
	}}
	log := &lockedLog{}
	cn := NewConnector(&fakeConnector{conn: native}, &fakeConnector{conn: ref}, Options{Log: log, ReferenceTimeout: timeout})
	dc, err := cn.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A transaction and a statement paired before the reference is lost.
	tx, err := dc.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := dc.(driver.ConnPrepareContext).PrepareContext(ctx, "q")
	if err != nil {
		t.Fatal(err)
	}
	exec := dc.(driver.ExecerContext)
	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := exec.ExecContext(ctx, "UPDATE t SET v = v", nil)
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
	case <-time.After(30 * time.Second):
		t.Fatal("dual ExecContext blocked on a stalled reference")
	}
	if _, err := exec.ExecContext(ctx, "SELECT 1", nil); err != nil {
		t.Fatal(err)
	}
	res, err := st.(driver.StmtExecContext).ExecContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("native statement result %d, %v", n, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := refCalls.Load(); n != 1 {
		t.Fatalf("reference calls %d, want 1", n)
	}
	close(release)
	released = true
	waitFor(t, "abandoned reference close", func() bool { return ref.closes.Load() == 1 })
	if err := dc.Close(); err != nil {
		t.Fatal(err)
	}

	rs := log.records(t)
	calls := make([]string, len(rs))
	for i, r := range rs {
		calls[i] = r["call"].(string)
	}
	if want := []string{"connect", "begin", "prepare", "exec", "exec", "stmt_exec", "stmt_close", "commit", "close"}; !slices.Equal(calls, want) {
		t.Fatalf("calls %q, want %q", calls, want)
	}
	for _, before := range rs[:3] {
		if before["unmirrored"] != nil || before["response_diff"] != nil || before["error_ref"] != "" {
			t.Errorf("record before the timeout: %v", before)
		}
	}
	const message = "reference did not respond within 1s; connection closed"
	timed := rs[3]
	if timed["error_ref"] != "08006" || timed["errmsg_ref"] != message || !slices.Equal(diffOf(timed), []string{message}) {
		t.Errorf("timed-out record: %v", timed)
	}
	if d, _ := timed["duration_ref_us"].(float64); d < float64(timeout.Microseconds()) {
		t.Errorf("duration_ref_us %v < %d", timed["duration_ref_us"], timeout.Microseconds())
	}
	for _, later := range rs[4:] {
		if later["unmirrored"] != "reference connection lost" || later["error_ref"] != "08006" || later["errmsg_ref"] != message || later["response_diff"] != nil {
			t.Errorf("later record: %v", later)
		}
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.writes) != len(rs) {
		t.Errorf("%d writes for %d records", len(log.writes), len(rs))
	}
	for _, w := range log.writes {
		if strings.Count(w, "\n") != 1 || !strings.HasSuffix(w, "\n") {
			t.Errorf("write is not one record line: %q", w)
		}
	}
}

func TestDualReferencePanic(t *testing.T) {
	ctx := context.Background()
	ref := &execConn{fakeConn: &fakeConn{}, query: func(context.Context, string) (driver.Rows, error) {
		panic("boom")
	}}
	native := &execConn{fakeConn: &fakeConn{},
		query: func(context.Context, string) (driver.Rows, error) { return oneRow(), nil },
		exec:  func(context.Context, string) (driver.Result, error) { return driver.RowsAffected(1), nil },
	}
	log := &lockedLog{}
	dc, err := NewConnector(&fakeConnector{conn: native}, &fakeConnector{conn: ref}, Options{Log: log}).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := dc.(driver.QueryerContext).QueryContext(ctx, "SELECT a", nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil || dest[0] != int64(1) {
		t.Fatalf("native row %v: %v", dest, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := dc.(driver.ExecerContext).ExecContext(ctx, "UPDATE t SET v = v", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "lost reference close", func() bool { return ref.closes.Load() == 1 })
	rs := log.records(t)
	if len(rs) != 3 {
		t.Fatalf("records %v", rs)
	}
	const message = "reference connection lost: panic: boom"
	if q := rs[1]; q["call"] != "query" || q["error_ref"] != "08006" || q["errmsg_ref"] != message || !slices.Equal(diffOf(q), []string{message}) {
		t.Errorf("panicked record: %v", q)
	}
	if e := rs[2]; e["call"] != "exec" || e["unmirrored"] != "reference connection lost" || e["response_diff"] != nil {
		t.Errorf("later record: %v", e)
	}
}
