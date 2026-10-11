package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGIndexesTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(db fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0)) }
			run(a, "CREATE TABLE pgi_tx_table(n INT,s TEXT)")
			run(a, "CREATE INDEX pgi_tx ON pgi_tx_table(n)")
			row := fixture.PgIndexesRow("public", "pgi_tx_table", "pgi_tx", "CREATE INDEX pgi_tx ON public.pgi_tx_table USING btree (n)")
			fixture.PgIndexesCheck(t, p, b, peer, "pgi_tx", row)
			run(a, "BEGIN")
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			run(a, "SAVEPOINT pgi_save")
			run(a, "ALTER TABLE pgi_tx_table DROP COLUMN n")
			fixture.PgIndexesCheck(t, p, b, a, "pgi_tx", nil)
			// pg_get_indexdef takes an AccessShareLock on the parent, conflicting
			// with the owner's ALTER. Prove committed peer identity without invoking
			// that deparser while the AccessExclusiveLock is held.
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT indexname FROM pg_catalog.pg_indexes WHERE schemaname='public' AND indexname='pgi_tx'", []string{"indexname"}, []string{"NAME"}, [][]any{{"pgi_tx"}}))
			run(a, "ROLLBACK TO pgi_save")
			fixture.PgIndexesCheck(t, p, b, a, "pgi_tx", row)
			run(a, "ROLLBACK")
			fixture.PgIndexesCheck(t, p, b, peer, "pgi_tx", row)
			run(a, "BEGIN")
			run(a, "ALTER TABLE pgi_tx_table DROP COLUMN n")
			run(a, "COMMIT")
			fixture.PgIndexesCheck(t, p, b, peer, "pgi_tx", nil)
		})
	}
}
func TestReferencePGIndexesRepeatableRead(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "InsideBegin", true: "BeforeBegin"}[before], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(db fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0)) }
					run(peer, "CREATE TABLE pgi_rr_table(n INT,s TEXT)")
					run(peer, "CREATE INDEX pgi_rr_old ON pgi_rr_table(n)")
					q := "SELECT * FROM pg_catalog.pg_indexes WHERE tablename='pgi_rr_table' AND schemaname='public' ORDER BY indexname"
					prepare := func() fixture.HeldStatement {
						stmt, err := a.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
						return fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
					}
					var held fixture.HeldStatement
					if before {
						held = prepare()
					}
					run(a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					if !before {
						held = prepare()
					}
					old := fixture.PgIndexesRow("public", "pgi_rr_table", "pgi_rr_old", "CREATE INDEX pgi_rr_old ON public.pgi_rr_table USING btree (n)")
					added := fixture.PgIndexesRow("public", "pgi_rr_table", "pgi_rr_added", "CREATE INDEX pgi_rr_added ON public.pgi_rr_table USING btree (s)")
					check := func(db fixture.Executor, rows [][]any) {
						fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.PgIndexesSelect(b, q, rows))
					}
					check(a, [][]any{old})
					check(held, [][]any{old})
					run(peer, "CREATE INDEX pgi_rr_added ON pgi_rr_table(s)")
					want := [][]any{old}
					if !b.PG {
						want = [][]any{added, old}
					}
					check(a, want)
					check(held, want)
					check(peer, [][]any{added, old})
					run(a, "COMMIT")
					check(a, [][]any{added, old})
					check(held, [][]any{added, old})
				})
			}
		})
	}
}
