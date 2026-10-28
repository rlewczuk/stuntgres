package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func arrayAggExecutionOwnership(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT array_agg($1::integer ORDER BY id) FROM aa_scalar")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	// Every worker returns its own independently expected result. The fixture's
	// bounded context owns teardown; no sleeps or completion-order assertions.
	results := make(chan error, 4)
	for i := 1; i <= 4; i++ {
		go func(i int) {
			rows, e := stmt.QueryContext(p.Ctx, int64(i))
			if e != nil {
				results <- e
				return
			}
			defer rows.Close()
			if !rows.Next() {
				results <- fmt.Errorf("worker %d missing row: %v", i, rows.Err())
				return
			}
			var raw any
			if e = rows.Scan(&raw); e != nil {
				results <- e
				return
			}
			want := fmt.Sprintf("{%d,%d,%d,%d,%d}", i, i, i, i, i)
			if fixture.Normalize(raw) != want {
				results <- fmt.Errorf("worker %d got %v want %s", i, raw, want)
				return
			}
			if rows.Next() {
				results <- fmt.Errorf("worker %d extra row", i)
				return
			}
			results <- rows.Err()
		}(i)
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if b.PG {
		return
	}
	// Native eager rows retain their payload and descriptors independently of
	// both source mutation and the prepared statement's lifetime.
	oldStmt, err := b.DB.PrepareContext(p.Ctx, "SELECT array_agg(v ORDER BY id) FROM aa_array")
	if err != nil {
		t.Fatal(err)
	}
	old, err := oldStmt.QueryContext(p.Ctx)
	if err != nil {
		oldStmt.Close()
		t.Fatal(err)
	}
	defer old.Close()
	fixture.ArrayAggMetadata(false, []string{"_INT4"})(t, old)
	if err := oldStmt.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE aa_array SET v='{9,10}' WHERE id=1", 1))
	fixture.ArrayReferenceConsumeHeld(t, old, [][]any{{"{{1,NULL},{3,4}}"}}, []string{"_INT4"})
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE aa_array SET v='{1,NULL}' WHERE id=1", 1))
	// Output environment belongs to the captured cursor, not a later session.
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET DateStyle='ISO, MDY'", 0))
	dateRows, err := conn.QueryContext(p.Ctx, "SELECT array_agg('2024-02-29'::date)")
	if err != nil {
		t.Fatal(err)
	}
	defer dateRows.Close()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET DateStyle='SQL, DMY'", 0))
	fixture.ArrayReferenceConsumeHeld(t, dateRows, [][]any{{"{2024-02-29}"}}, []string{"_DATE"})
}
