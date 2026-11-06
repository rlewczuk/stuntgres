package reference_test

import (
	"database/sql"
	"errors"
	"slices"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

// dualReadSets reads every row of every set as text; limit > 0 reads at most
// limit rows of the first set before moving on.
func dualReadSets(t *testing.T, rows *sql.Rows, limit int) ([][]string, error) {
	t.Helper()
	var sets [][]string
	for {
		var set []string
		cols, err := rows.Columns()
		if err != nil {
			return sets, err
		}
		for read := 0; rows.Next(); {
			values := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				set = append(set, v.String)
			}
			if read++; len(sets) == 0 && limit > 0 && read == limit {
				break
			}
		}
		sets = append(sets, set)
		if !rows.NextResultSet() {
			return sets, rows.Err()
		}
	}
}

func TestReferenceDualConnectorMultiResult(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	query := func(q string, limit int) ([][]string, dualRecord, error) {
		t.Helper()
		rows, err := d.DB.QueryContext(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		sets, err := dualReadSets(t, rows, limit)
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		return sets, lastQuery(t, d.Log.records(t), "query", q), err
	}

	sets, r, err := query("SELECT 1 AS a; SELECT 'x' AS b, 2 AS c", 0)
	if err != nil || !slices.EqualFunc(sets, [][]string{{"1"}, {"x", "2"}}, slices.Equal) {
		t.Fatalf("two sets %q, %v", sets, err)
	}
	wantDiff(t, r)
	if n, ref := decodeSets(t, r.Response), decodeSets(t, r.ResponseRef); len(n.Sets) != 2 || len(ref.Sets) != 2 || !n.Complete || !ref.Complete {
		t.Errorf("set summaries %+v / %+v", n, ref)
	}

	for _, table := range []string{"dual_m1", "dual_m2"} {
		dualExec(t, ctx, d.DB, "CREATE TABLE "+table+" (id int)")
		dualExec(t, ctx, d.DB, "INSERT INTO "+table+" VALUES (1), (2)")
	}
	dualExec(t, ctx, d.Ref, "INSERT INTO dual_m2 VALUES (3)")
	sets, r, err = query("SELECT id FROM dual_m1 ORDER BY id; SELECT id FROM dual_m2 ORDER BY id", 0)
	if err != nil || !slices.EqualFunc(sets, [][]string{{"1", "2"}, {"1", "2"}}, slices.Equal) {
		t.Fatalf("table sets %q, %v", sets, err)
	}
	wantDiff(t, r, "#1 rows: stuntgres 2, reference 3", `#1 reference only row 2 ["3"]`)

	// The caller leaves set 0 after one row; native skips the unread rows.
	sets, r, err = query("SELECT g FROM generate_series(1, 3) g; SELECT 4", 1)
	if err != nil || !slices.EqualFunc(sets, [][]string{{"1"}, {"4"}}, slices.Equal) {
		t.Fatalf("partial sets %q, %v", sets, err)
	}
	wantDiff(t, r)
	n, ref := decodeSets(t, r.Response), decodeSets(t, r.ResponseRef)
	if len(n.Sets) != 2 || n.Sets[0].Rows != 1 || !n.Sets[0].Partial || len(ref.Sets) != 2 || ref.Sets[0].Rows != 3 || ref.Sets[0].Partial {
		t.Errorf("partial summaries %+v / %+v", n, ref)
	}

	// A later statement's error is deferred to iteration on both sides.
	sets, r, err = query("SELECT 1 AS a; SELECT * FROM dual_missing", 0)
	if code, message := dualSQLState(err); code != "42P01" || message != `relation "dual_missing" does not exist` || len(sets) == 0 || !slices.Equal(sets[0], []string{"1"}) {
		t.Fatalf("deferred error sets %q, %v", sets, err)
	}
	if r.Error != "42P01" || r.ErrorRef != "42P01" {
		t.Errorf("deferred error record: %q / %q", r.Error, r.ErrorRef)
	}
	wantDiff(t, r)

	const batch = "INSERT INTO dual_m1 VALUES (5); INSERT INTO dual_m1 VALUES (6), (7)"
	if n := dualExec(t, ctx, d.DB, batch); n != 2 {
		t.Fatalf("batch rows affected %d, want 2 (last command)", n)
	}
	r = lastQuery(t, d.Log.records(t), "exec", batch)
	wantDiff(t, r)
	wantJSON(t, "batch response", r.Response, `{"rows_affected":2,"last_insert_id":null}`)
	wantJSON(t, "batch response_ref", r.ResponseRef, `{"rows_affected":2,"last_insert_id":null}`)
}

func TestReferenceDualConnectorResultAvailability(t *testing.T) {
	p := fixture.NewScenario(t)
	fault := newDualFault()
	d := newDualPair(t, p, dual.Options{}, fault.wrap)
	ctx := p.Ctx
	dualExec(t, ctx, d.DB, "CREATE TABLE dual_r (id int)")
	const insert = "INSERT INTO dual_r VALUES (1)"
	fault.mu.Lock()
	fault.result[insert] = dualFaultResult{affectedErr: errors.New("injected"), insertID: 7}
	fault.mu.Unlock()
	if n := dualExec(t, ctx, d.DB, insert); n != 1 {
		t.Fatalf("caller rows affected %d, want 1", n)
	}
	r := lastQuery(t, d.Log.records(t), "exec", insert)
	wantDiff(t, r, "rows_affected: stuntgres 1, reference unavailable", "last_insert_id: stuntgres unavailable, reference 7")
	wantJSON(t, "response_ref", r.ResponseRef, `{"rows_affected":null,"last_insert_id":7}`)
	if n := dualCount(t, ctx, d.Ref, "dual_r"); n != 1 {
		t.Errorf("reference count %d, want 1: the statement ran on the reference", n)
	}
}
