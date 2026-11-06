package reference_test

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDualConnectorDifferences(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	c, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	dualExec(t, ctx, c, "CREATE TABLE dual_d (id int, v text)")
	dualExec(t, ctx, c, "INSERT INTO dual_d VALUES (1, 'a'), (2, 'b')")
	const query = "SELECT id, v FROM dual_d ORDER BY id"
	selectAll := func() dualRecord {
		t.Helper()
		rows, err := c.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var id int64
			var v string
			if err := rows.Scan(&id, &v); err != nil {
				t.Fatal(err)
			}
			got = append(got, fmt.Sprint(id, " ", v))
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if want := []string{"1 a", "2 b"}; !slices.Equal(got, want) {
			t.Fatalf("caller rows %q, want %q", got, want)
		}
		return lastQuery(t, d.Log.records(t), "query", query)
	}

	dualExec(t, ctx, d.Ref, "UPDATE dual_d SET v = 'z' WHERE id = 2")
	r := selectAll()
	wantDiff(t, r, `#0 row 1 v: stuntgres "b", reference "z"`)
	if r.OrderOnly {
		t.Error("value difference flagged order_only")
	}

	dualExec(t, ctx, d.Ref, "INSERT INTO dual_d VALUES (3, 'c')")
	wantDiff(t, selectAll(), "#0 rows: stuntgres 2, reference 3", `#0 row 1 v: stuntgres "b", reference "z"`, `#0 reference only row 2 ["3","c"]`)

	// The same difference in a pool that does not compare queries.
	quiet := newDualPair(t, p, dual.Options{Uncompared: []dual.Call{dual.CallQuery}}, nil)
	if got := dualStrings(t, ctx, quiet.DB, "SELECT v FROM dual_d ORDER BY id"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("uncompared caller rows %q", got)
	}
	if r := lastQuery(t, quiet.Log.records(t), "query", "SELECT v FROM dual_d ORDER BY id"); r.Uncompared != "option" || r.ResponseDiff != nil {
		t.Errorf("uncompared record: uncompared %q diff %q", r.Uncompared, r.ResponseDiff)
	}

	if n := dualExec(t, ctx, c, "UPDATE dual_d SET v = v"); n != 2 {
		t.Fatalf("caller rows affected %d, want 2", n)
	}
	wantDiff(t, lastQuery(t, d.Log.records(t), "exec", "UPDATE dual_d SET v = v"), "rows_affected: stuntgres 2, reference 3")

	// Reference-only errors: direct and prepared.
	dualExec(t, ctx, d.Ref, "DROP TABLE dual_d")
	const missing = `error: stuntgres none, reference 42P01 relation "dual_d" does not exist`
	const one = "SELECT v FROM dual_d WHERE id = 1"
	if got := dualStrings(t, ctx, c, one); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("caller value %q, want a", got)
	}
	r = lastQuery(t, d.Log.records(t), "query", one)
	if r.ErrorRef != "42P01" || r.ErrmsgRef != `relation "dual_d" does not exist` || r.Error != "" {
		t.Errorf("reference-only error record: error %q error_ref %q errmsg_ref %q", r.Error, r.ErrorRef, r.ErrmsgRef)
	}
	wantDiff(t, r, missing)
	const prepared = "SELECT v FROM dual_d WHERE id = $1"
	stmt, err := c.PrepareContext(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := stmt.QueryRowContext(ctx, 1).Scan(&v); err != nil || v != "a" {
		t.Fatalf("prepared caller value %q, %v", v, err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	rs := d.Log.records(t)
	if r := lastQuery(t, rs, "prepare", prepared); r.ErrorRef != "42P01" {
		t.Errorf("prepare error_ref %q", r.ErrorRef)
	} else {
		wantDiff(t, r, missing)
	}
	for _, call := range []string{"stmt_query", "stmt_close"} {
		if r := lastQuery(t, rs, call, prepared); r.Unmirrored != "no reference statement" || r.ResponseDiff != nil || r.ErrorRef != "" {
			t.Errorf("%s record: unmirrored %q diff %q error_ref %q", call, r.Unmirrored, r.ResponseDiff, r.ErrorRef)
		}
	}

	// A statement that fails only on the reference.
	dualExec(t, ctx, d.Ref, "CREATE TABLE dual_only_ref (x int)")
	dualExec(t, ctx, c, "CREATE TABLE dual_only_ref (x int)")
	r = lastQuery(t, d.Log.records(t), "exec", "CREATE TABLE dual_only_ref (x int)")
	if r.ErrorRef != "42P07" {
		t.Errorf("error_ref %q, want 42P07", r.ErrorRef)
	}
	wantDiff(t, r, `error: stuntgres none, reference 42P07 relation "dual_only_ref" already exists`)

	// A statement that fails on both sides: equal errors, the native error
	// value reaches the caller unwrapped.
	const absent = "SELECT * FROM dual_absent"
	_, dualErr := c.QueryContext(ctx, absent)
	_, bareErr := d.Native.QueryContext(ctx, absent)
	if code, message := dualSQLState(dualErr); code != "42P01" || message != `relation "dual_absent" does not exist` {
		t.Errorf("caller error %q %q (%v)", code, message, dualErr)
	}
	var native *stuntgres.Error
	if !errors.As(dualErr, &native) || dualErr.Error() != `stuntgres: relation "dual_absent" does not exist` {
		t.Errorf("caller error is not the native diagnostic: %T %v", dualErr, dualErr)
	}
	if fmt.Sprintf("%T %v", dualErr, dualErr) != fmt.Sprintf("%T %v", bareErr, bareErr) {
		t.Errorf("caller error %T %v differs from the bare driver's %T %v", dualErr, dualErr, bareErr, bareErr)
	}
	r = lastQuery(t, d.Log.records(t), "query", absent)
	if r.Error != "42P01" || r.ErrorRef != "42P01" || r.ResponseDiff != nil {
		t.Errorf("both-side error record: error %q error_ref %q diff %q", r.Error, r.ErrorRef, r.ResponseDiff)
	}
}
