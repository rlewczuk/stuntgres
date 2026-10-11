package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Moved from the reference-tagged capability file so the supported simple
// indexdef assertion executes in quick mode as well as pinned PostgreSQL.
func TestReferenceAlterTypeCompatiblePlainIndexBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(3),(1),(2),(NULL)", 4))
			run(fixture.Exec("CREATE INDEX ix ON t(c)", 0))
			var oldID int64
			if b.PG {
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ix'").Scan(&oldID); err != nil || oldID <= 0 {
					t.Fatal(oldID, err)
				}
			}
			run(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
			if b.PG {
				var newID int64
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ix'").Scan(&newID); err != nil || newID <= 0 || newID == oldID {
					t.Fatal("compatible rewrite must rebuild index identity", oldID, newID, err)
				}
				run(fixture.Select("SELECT count(*)::int FROM pg_class WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, oldID))
				run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='ix'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
			}
			run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
			run(fixture.Select("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (c)"}}))
			if b.PG {
				run(fixture.Exec("SET enable_seqscan=off", 0))
				run(fixture.Select("EXPLAIN (COSTS OFF) SELECT c FROM t WHERE c=2", []string{"QUERY PLAN"}, []string{"TEXT"}, [][]any{{"Index Only Scan using ix on t"}, {"  Index Cond: (c = 2)"}}))
				run(fixture.Select("SELECT c FROM t WHERE c=2", []string{"c"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(fixture.Exec("RESET enable_seqscan", 0))
				run(fixture.Select("SELECT atttypid::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"atttypid"}, []string{"INT4"}, [][]any{{int64(20)}}))
			} else {
				run(fixture.Select("SELECT relkind FROM pg_class WHERE relname='ix'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
			}
		})
	}
}
