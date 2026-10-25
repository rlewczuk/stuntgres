package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintTransactions(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc_tx(a INT,CONSTRAINT pc_tx_old CHECK(a>0))")
		id := pgConstraintID(t, p, b.DB, "pc_tx", "pc_tx_old")
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		q := "SELECT conname,conkey::text,convalidated,conenforced FROM pg_constraint WHERE conrelid='pc_tx'::regclass::oid ORDER BY conname"
		check := func(db fixture.Executor, names ...string) {
			want := make([][]any, len(names))
			for i, name := range names {
				want[i] = []any{name, "{1}", true, true}
			}
			pgConstraintCheck(t, p, b, db, fixture.Select(q, []string{"conname", "conkey", "convalidated", "conenforced"}, []string{"NAME", "TEXT", "BOOL", "BOOL"}, want))
		}
		fixture.PGClassExec(t, p, b, owner, "BEGIN", "SAVEPOINT pc_sp", "ALTER TABLE pc_tx RENAME CONSTRAINT pc_tx_old TO pc_tx_renamed", "ALTER TABLE pc_tx ADD CONSTRAINT pc_tx_add CHECK(a<10)")
		check(owner, "pc_tx_add", "pc_tx_renamed")
		check(peer, "pc_tx_old")
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK TO pc_sp")
		check(owner, "pc_tx_old")
		if got := pgConstraintID(t, p, owner, "pc_tx", "pc_tx_old"); got != id {
			t.Fatal("savepoint identity", got, id)
		}
		fixture.PGClassExec(t, p, b, owner, "ALTER TABLE pc_tx DROP CONSTRAINT pc_tx_old")
		check(owner)
		check(peer, "pc_tx_old")
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK")
		check(owner, "pc_tx_old")
		if got := pgConstraintID(t, p, owner, "pc_tx", "pc_tx_old"); got != id {
			t.Fatal("rollback identity", got, id)
		}
		fixture.PGClassExec(t, p, b, owner, "BEGIN", "ALTER TABLE pc_tx RENAME CONSTRAINT pc_tx_old TO pc_tx_committed")
		check(peer, "pc_tx_old")
		fixture.PGClassExec(t, p, b, owner, "COMMIT")
		check(peer, "pc_tx_committed")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO pc_tx VALUES(1),(1)", 2))
		failure := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "pc_tx_unique"`)
		failure.Detail, failure.Schema, failure.Table, failure.Constraint = "Key (a)=(1) is duplicated.", "public", "pc_tx", "pc_tx_unique"
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.ErrorDiagnostic("ALTER TABLE pc_tx DROP CONSTRAINT pc_tx_committed, ADD CONSTRAINT pc_tx_unique UNIQUE(a)", failure))
		check(owner, "pc_tx_committed")
		if got := pgConstraintID(t, p, owner, "pc_tx", "pc_tx_committed"); got != id {
			t.Fatal("failed ALTER identity", got, id)
		}
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT a FROM pc_tx ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		pgConstraintCheck(t, p, b, owner, fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_tx_unique'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.PGClassExec(t, p, b, owner, "CREATE TABLE pc_tx_deferred(a INT,CONSTRAINT pc_tx_x EXCLUDE USING btree(a WITH =) DEFERRABLE INITIALLY DEFERRED)", "BEGIN", "SET CONSTRAINTS pc_tx_x IMMEDIATE")
		flags := fixture.Select("SELECT condeferrable,condeferred FROM pg_constraint WHERE conrelid='pc_tx_deferred'::regclass::oid", []string{"condeferrable", "condeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		pgConstraintCheck(t, p, b, owner, flags)
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK")
		pgConstraintCheck(t, p, b, owner, flags)
	})
}
