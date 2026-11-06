package reference_test

import (
	"database/sql/driver"
	"slices"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDualConnectorEarlyClose(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	fault := newDualFault()
	d := newDualPair(t, p, dual.Options{}, fault.wrap)
	// readIDs reads n rows of query and closes the rows early.
	readIDs := func(query string, n int) ([]int64, dualRecord) {
		t.Helper()
		rows, err := d.DB.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if open := fault.openRows(); open != 0 {
			t.Errorf("%d reference rows open after Query returned", open)
		}
		var got []int64
		for len(got) < n && rows.Next() {
			var v int64
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		return got, lastQuery(t, d.Log.records(t), "query", query)
	}

	const series = "SELECT g FROM generate_series(1, 5) g ORDER BY g"
	got, r := readIDs(series, 2)
	if !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("caller rows %v", got)
	}
	wantDiff(t, r)
	wantJSON(t, "native summary", r.Response, `{"sets":[{"columns":["g"],"types":["INT4"],"rows":2,"partial":true}],"complete":false}`)
	wantJSON(t, "reference summary", r.ResponseRef, `{"sets":[{"columns":["g"],"types":["INT4"],"rows":5,"partial":false}],"complete":true}`)

	dualExec(t, ctx, d.DB, "CREATE TABLE dual_e (id int)")
	dualExec(t, ctx, d.DB, "INSERT INTO dual_e SELECT generate_series(1, 5)")
	dualExec(t, ctx, d.Ref, "UPDATE dual_e SET id = 40 WHERE id = 4")
	const ids = "SELECT id FROM dual_e ORDER BY id"
	if got, r := readIDs(ids, 2); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("caller rows %v", got)
	} else {
		wantDiff(t, r) // unread rows are not compared
	}
	if got, r := readIDs(ids, 4); !slices.Equal(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("caller rows %v", got)
	} else {
		wantDiff(t, r, `#0 row 3 id: stuntgres "4", reference "5"`)
	}
	if err := d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if open := fault.openConns(); open != 0 {
		t.Errorf("%d reference connections open after the pool closed", open)
	}
}

func TestReferenceDualConnectorSessionReset(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	fault := newDualFault()
	d := newDualPair(t, p, dual.Options{}, fault.wrap)
	d.DB.SetMaxOpenConns(1)
	for _, want := range []int64{1, 1} {
		if n := dualExec(t, ctx, d.DB, "SELECT 1"); n != want {
			t.Fatalf("rows affected %d, want %d", n, want)
		}
	}
	rs := d.Log.records(t)
	if got, want := dualCalls(rs), []string{"connect", "exec", "is_valid", "reset_session", "exec", "is_valid"}; !slices.Equal(got, want) {
		t.Fatalf("calls %q, want %q", got, want)
	}
	reset := rs[3]
	if reset.Unmirrored != "" || reset.ResponseDiff != nil || reset.ErrorRef != "" {
		t.Errorf("reset_session record: %+v", reset)
	}
	wantJSON(t, "reset response_ref", reset.ResponseRef, `{}`)
	if rs[1].ConnectionID != rs[4].ConnectionID {
		t.Errorf("execs on connections %d and %d", rs[1].ConnectionID, rs[4].ConnectionID)
	}

	fault.mu.Lock()
	fault.resetErr = driver.ErrBadConn
	fault.mu.Unlock()
	if n := dualExec(t, ctx, d.DB, "SELECT 1"); n != 1 {
		t.Fatalf("rows affected %d after the reference reset failed", n)
	}
	rs = d.Log.records(t)[len(rs):]
	if got, want := dualCalls(rs), []string{"reset_session", "exec", "is_valid"}; !slices.Equal(got, want) {
		t.Fatalf("calls %q, want %q", got, want)
	}
	if rs[0].ErrorRef != "08006" {
		t.Errorf("failed reset error_ref %q", rs[0].ErrorRef)
	}
	wantDiff(t, rs[0], "reference connection lost: driver: bad connection")
	if rs[1].ConnectionID != rs[0].ConnectionID || rs[1].Unmirrored != "reference connection lost" || rs[1].ResponseDiff != nil {
		t.Errorf("exec after the failed reset: %+v", rs[1])
	}
}
