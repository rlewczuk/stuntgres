package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInheritsTransactions(t *testing.T) {
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB,
			"CREATE TABLE pi_tx_root(k INT,payload TEXT,CONSTRAINT pi_tx_key UNIQUE(k,payload)) PARTITION BY RANGE(k)",
			"CREATE TABLE pi_tx_child(k INT,payload TEXT,CONSTRAINT pi_tx_child_idx UNIQUE(k,payload))")
		r, c := pgInheritsID(t, p, b.DB, "pi_tx_root"), pgInheritsID(t, p, b.DB, "pi_tx_child")
		ri, ci := pgInheritsID(t, p, b.DB, "pi_tx_key"), pgInheritsID(t, p, b.DB, "pi_tx_child_idx")
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		ha, hb := pgInheritsHeld(t, p, a), pgInheritsHeld(t, p, peer)
		check := func(owner, public bool) {
			for _, item := range []struct {
				held   fixture.HeldStatement
				linked bool
			}{{ha, owner}, {hb, public}} {
				var table, index [][]any
				if item.linked {
					table = [][]any{pgInheritsEdge(c, r, 1)}
					index = [][]any{pgInheritsEdge(ci, ri, 1)}
				}
				pgInheritsHeldRows(t, p, b, item.held, c, table)
				pgInheritsHeldRows(t, p, b, item.held, ci, index)
			}
		}
		check(false, false)
		fixture.PGClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pi_attach", "ALTER TABLE pi_tx_root ATTACH PARTITION pi_tx_child FOR VALUES FROM(0) TO(5)")
		check(true, false)
		fixture.PGClassExec(t, p, b, a, "ROLLBACK TO pi_attach")
		check(false, false)
		fixture.PGClassExec(t, p, b, a, "ALTER TABLE pi_tx_root ATTACH PARTITION pi_tx_child FOR VALUES FROM(0) TO(5)", "COMMIT")
		check(true, true)
		fixture.PGClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pi_detach", "ALTER TABLE pi_tx_root DETACH PARTITION pi_tx_child")
		check(false, true)
		fixture.PGClassExec(t, p, b, a, "ROLLBACK TO pi_detach")
		check(true, true)
		fixture.PGClassExec(t, p, b, a, "ALTER TABLE pi_tx_root DETACH PARTITION pi_tx_child", "COMMIT")
		check(false, false)

		pgInheritsManualTree(t, p, b)
		mi, ai := pgInheritsID(t, p, a, "pi_manual_idx"), pgInheritsID(t, p, a, "pi_a_idx")
		fixture.PGClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pi_index", "ALTER INDEX pi_manual_idx ATTACH PARTITION pi_a_idx")
		pgInheritsHeldRows(t, p, b, ha, ai, [][]any{pgInheritsEdge(ai, mi, 1)})
		pgInheritsHeldRows(t, p, b, hb, ai, nil)
		fixture.PGClassExec(t, p, b, a, "ROLLBACK TO pi_index")
		pgInheritsHeldRows(t, p, b, ha, ai, nil)
		fixture.PGClassExec(t, p, b, a, "ALTER INDEX pi_manual_idx ATTACH PARTITION pi_a_idx", "COMMIT")
		pgInheritsHeldRows(t, p, b, hb, ai, [][]any{pgInheritsEdge(ai, mi, 1)})
		fixture.PGClassExec(t, p, b, a, "CREATE UNIQUE INDEX pi_wrong_idx ON pi_b(payload,k)")
		fail := fixture.ImplicitError("ALTER INDEX pi_manual_idx ATTACH PARTITION pi_wrong_idx", fixture.Diagnostic{Code: "42P17", Message: `cannot attach index "pi_wrong_idx" as a partition of index "pi_manual_idx"`, Detail: "The index definitions do not match."})
		for _, prepared := range []bool{false, true} {
			fail.Prepared, fail.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fail)
			pgInheritsHeldRows(t, p, b, ha, ai, [][]any{pgInheritsEdge(ai, mi, 1)})
			pgInheritsBag(t, p, b, a, []string{"pi_wrong_idx"}, nil)
		}
		fixture.PGClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pi_failure")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fail)
		fixture.PGClassExec(t, p, b, a, "ROLLBACK TO pi_failure")
		pgInheritsHeldRows(t, p, b, ha, ai, [][]any{pgInheritsEdge(ai, mi, 1)})
		fixture.PGClassExec(t, p, b, a, "COMMIT")
		if pgInheritsID(t, p, a, "pi_a_idx") != ai || pgInheritsID(t, p, a, "pi_manual_idx") != mi {
			t.Fatal("index attachment changed identity")
		}
	})
}

func TestReferencePGInheritsIdentityAndDrop(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pi_move")
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB, "CREATE SCHEMA pi_move", "CREATE TABLE pi_p(a INT)", "CREATE TABLE pi_c() INHERITS(pi_p)", "CREATE TABLE pi_other(a INT)", "CREATE TABLE pi_survivor() INHERITS(pi_other)")
		parent, child := pgInheritsID(t, p, b.DB, "pi_p"), pgInheritsID(t, p, b.DB, "pi_c")
		other, survivor := pgInheritsID(t, p, b.DB, "pi_other"), pgInheritsID(t, p, b.DB, "pi_survivor")
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		held := pgInheritsHeld(t, p, owner)
		want := [][]any{pgInheritsEdge(child, parent, 1)}
		pgInheritsHeldRows(t, p, b, held, child, want)
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("DROP TABLE pi_p RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table pi_p because other objects depend on it", Detail: "table pi_c depends on table pi_p", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
			pgInheritsHeldRows(t, p, b, held, child, want)
		}
		fixture.PGClassExec(t, p, b, owner, "ALTER TABLE pi_p RENAME TO pi_renamed", "ALTER TABLE pi_renamed SET SCHEMA pi_move")
		if pgInheritsID(t, p, owner, "pi_move.pi_renamed") != parent {
			t.Fatal("rename changed identity")
		}
		pgInheritsHeldRows(t, p, b, held, child, want)
		fixture.PGClassExec(t, p, b, owner, "DROP TABLE pi_c", "CREATE TABLE pi_c() INHERITS(pi_move.pi_renamed)")
		fresh := pgInheritsID(t, p, owner, "pi_c")
		if fresh == child {
			t.Fatal("recreated child reused identity")
		}
		pgInheritsHeldRows(t, p, b, held, child, nil)
		pgInheritsHeldRows(t, p, b, held, fresh, [][]any{pgInheritsEdge(fresh, parent, 1)})
		fixture.PGClassExec(t, p, b, owner, "DROP TABLE pi_move.pi_renamed CASCADE")
		pgInheritsHeldRows(t, p, b, held, fresh, nil)
		pgInheritsHeldRows(t, p, b, held, survivor, [][]any{pgInheritsEdge(survivor, other, 1)})
		fixture.PGClassExec(t, p, b, owner, "DROP SCHEMA pi_move")
	})
}
