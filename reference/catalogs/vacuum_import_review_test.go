package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Native public controls have no PostgreSQL API counterpart. This is not an
// oracle-discovery filter: this native witness always executes in both modes.
func TestReferenceVacuumF3ImportedStatistics(t *testing.T) {
	p := fixture.NewFixture(t, false, true)
	b := p.Backends()[0]
	c := vacuumConn(t, p, b)
	run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE vacuum_review_import(id INT)", 0), fixture.Exec("INSERT INTO vacuum_review_import VALUES(1),(2)", 2), fixture.Exec("VACUUM vacuum_review_import", 0)} {
		run(s)
	}
	owned, err := c.QueryContext(p.Ctx, "SELECT id FROM vacuum_review_import ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	check := func(live, dead, inserts, deletes, mods, since int64) {
		run(fixture.Select("SELECT n_live_tup,n_dead_tup,n_tup_ins,n_tup_upd,n_tup_del,n_mod_since_analyze,n_ins_since_vacuum,vacuum_count,last_vacuum IS NULL AS no_history FROM pg_stat_all_tables WHERE relid='vacuum_review_import'::regclass", []string{"n_live_tup", "n_dead_tup", "n_tup_ins", "n_tup_upd", "n_tup_del", "n_mod_since_analyze", "n_ins_since_vacuum", "vacuum_count", "no_history"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{live, dead, inserts, int64(0), deletes, mods, since, int64(0), true}}))
		run(fixture.Select("SELECT reltuples=-1::real AS unscanned FROM pg_class WHERE oid='vacuum_review_import'::regclass", []string{"unscanned"}, []string{"BOOL"}, [][]any{{true}}))
	}
	// Assert current facts before any VACUUM can repair an imported baseline.
	check(2, 0, 0, 0, 0, 0)
	vacuumRows(t, p, b, c, "vacuum_review_import", 1, 2)
	run(fixture.Exec("DELETE FROM vacuum_review_import WHERE id=1", 1))
	check(1, 1, 0, 1, 1, 0)
	run(fixture.Exec("INSERT INTO vacuum_review_import VALUES(7)", 1))
	check(2, 1, 1, 1, 2, 1)
	vacuumRows(t, p, b, c, "vacuum_review_import", 2, 7)
	run(fixture.Exec("VACUUM vacuum_review_import", 0))
	vacuumCount(t, p.Ctx, c, "vacuum_review_import", 1)
	run(fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='vacuum_review_import'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(0)}}))
	for _, want := range []int{1, 2} {
		var got int
		if !owned.Next() {
			t.Fatal("replacement invalidated retained result", owned.Err())
		}
		if err := owned.Scan(&got); err != nil || got != want {
			t.Fatal("retained import result", got, want, err)
		}
	}
	if owned.Next() || owned.Err() != nil {
		t.Fatal("retained import result tail", owned.Err())
	}
}
