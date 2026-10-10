package reference_test

import "testing"

func partialIndexScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, func(referenceStep))) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			body(t, p, b, run)
		})
	}
}

func partialIndexSeed(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE t(id INT,k INT,active BOOL)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,10,TRUE),(2,20,FALSE),(3,30,NULL),(4,NULL,TRUE),(5,10,TRUE)", 5))
}

func partialIndexIDs(q string, ids ...int64) referenceStep {
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return referenceSelect(q, []string{"id"}, []string{"INT4"}, rows)
}

func TestReferencePartialIndexMembership(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				partialIndexSeed(run)
				for _, q := range []string{
					"CREATE INDEX pi ON t(k) WHERE active",
					"CREATE INDEX nulls ON t(k) WHERE active IS NULL",
					"CREATE INDEX all_rows ON t(k) WHERE TRUE",
					"CREATE INDEX no_rows ON t(k) WHERE FALSE",
					"CREATE INDEX unknown_rows ON t(k) WHERE NULL",
					"CREATE INDEX compound ON t((k+1) int4_ops,id DESC) INCLUDE(active) WHERE active AND k=10",
				} {
					s := referenceExec(q, 0)
					if held {
						s = referencePrepared(s)
					}
					run(s)
				}
				for _, s := range []referenceStep{
					partialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5),
					partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5),
					partialIndexIDs("SELECT id FROM t WHERE active IS NOT TRUE ORDER BY id", 2, 3),
					partialIndexIDs("SELECT id FROM t WHERE active IS NULL ORDER BY id", 3),
					partialIndexIDs("SELECT id FROM t WHERE active AND k=10 ORDER BY id", 1, 5),
					partialIndexIDs("SELECT id FROM t WHERE FALSE ORDER BY id"),
					partialIndexIDs("SELECT id FROM t WHERE NULL ORDER BY id"),
					referenceSelect("SELECT id,k,active FROM t ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(10), true}, {int64(2), int64(20), false}, {int64(3), int64(30), nil}, {int64(4), nil, true}, {int64(5), int64(10), true}}),
				} {
					run(s)
					run(referencePrepared(s))
				}
			})
		})
	}
}

func TestReferencePartialIndexTransitions(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		partialIndexSeed(run)
		run(referenceExec("CREATE INDEX pi ON t(k) WHERE active", 0))
		run(referencePrepared(referenceExec("UPDATE t SET active=TRUE WHERE id=2 OR id=3", 2)))
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 3, 4, 5))
		run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
		run(referenceExec("UPDATE t SET active=NULL WHERE id=4", 1))
		run(referencePrepared(referenceExec("UPDATE t SET k=11 WHERE id=5", 1)))
		run(referenceExec("UPDATE t SET k=99 WHERE id=1", 1))
		run(referenceExec("INSERT INTO t VALUES(6,60,TRUE),(7,70,FALSE),(8,80,NULL)", 3))
		run(referencePrepared(referenceExec("INSERT INTO t SELECT id+10,k,active FROM t WHERE id=6 OR id=7", 2)))
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 6, 16))
		run(referencePrepared(referenceExec("DELETE FROM t WHERE id=6 OR id=7", 2)))
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 16))
		tx, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		trun(referenceExec("UPDATE t SET active=TRUE WHERE id=1", 1))
		trun(referenceExec("SAVEPOINT s", 0))
		trun(referenceExec("DELETE FROM t WHERE active", 5))
		trun(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id"))
		trun(referenceExec("ROLLBACK TO s", 0))
		trun(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 3, 5, 16))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 16))
		tx, err = b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("UPDATE t SET active=TRUE WHERE id=8", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 8, 16))
	})
}

func TestReferencePartialIndexPredicateFirst(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(id INT,n INT,active BOOL)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,0,FALSE),(2,2,TRUE),(3,0,NULL),(4,2,FALSE)", 4))
				create := referenceExec("CREATE INDEX pi ON t((10/n)) WHERE active", 0)
				if held {
					create = referencePrepared(create)
				}
				run(create)
				run(referenceExec("INSERT INTO t VALUES(5,0,FALSE),(6,2,TRUE)", 2))
				for _, q := range []string{"UPDATE t SET active=TRUE WHERE id=1 OR id=4", "INSERT INTO t VALUES(7,2,TRUE),(8,0,TRUE)"} {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
					s.errorStage = "execute"
					if held {
						s = referencePrepared(s)
					}
					run(s)
					run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 6))
					run(partialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5, 6))
				}
				run(referenceExec("UPDATE t SET active=TRUE WHERE id=4", 1))
				run(referenceSelect("SELECT id,10/n AS key FROM t WHERE active ORDER BY id", []string{"id", "key"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(4), int64(5)}, {int64(6), int64(5)}}))
				run(referenceImplicitError("CREATE INDEX broken ON t(id) WHERE 10/n>0", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='broken'", []string{"relname"}, []string{"NAME"}, nil))
				run(referenceExec("CREATE TABLE predicates(id INT,n INT)", 0))
				run(referenceExec("INSERT INTO predicates VALUES(1,2)", 1))
				run(referenceExec("CREATE INDEX pred ON predicates(id) WHERE 10/n>0", 0))
				run(referenceImplicitError("INSERT INTO predicates VALUES(2,2),(3,0)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
				run(partialIndexIDs("SELECT id FROM predicates ORDER BY id", 1))
				tx, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT s", 0))
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("UPDATE predicates SET n=0", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO s", 0))
				referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT n FROM predicates", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestReferencePartialIndexParameters(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		partialIndexSeed(run)
		run(referenceExec("CREATE INDEX fixed ON t(k) WHERE k=10", 0))
		run(referenceExec("CREATE INDEX active_rows ON t(k) WHERE active", 0))
		for _, q := range []string{"SELECT id FROM t WHERE k=$1 ORDER BY id", "SELECT id FROM t WHERE active AND k=$1 ORDER BY id"} {
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				arg any
				ids []int64
			}{{int64(10), []int64{1, 5}}, {int64(20), []int64{2}}, {int64(30), []int64{3}}, {nil, nil}, {int64(10), []int64{1, 5}}} {
				ids := tc.ids
				if q == "SELECT id FROM t WHERE active AND k=$1 ORDER BY id" && tc.arg != int64(10) {
					ids = nil
				}
				s := partialIndexIDs(q, ids...)
				s.args = []any{tc.arg}
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
			}
		}
		run(partialIndexIDs("SELECT id FROM t WHERE active OR k=20 ORDER BY id", 1, 2, 4, 5))
		run(partialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5))
		run(referenceExec("CREATE INDEX bound ON t(k) WHERE k<20", 0))
		q := "SELECT id FROM t WHERE k<$1 ORDER BY id"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg int64
			ids []int64
		}{{20, []int64{1, 5}}, {40, []int64{1, 2, 3, 5}}, {10, nil}, {40, []int64{1, 2, 3, 5}}} {
			s := partialIndexIDs(q, tc.ids...)
			s.args = []any{tc.arg}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
		}
		tx, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("INSERT INTO t VALUES(6,10,TRUE)", 1))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("UPDATE t SET active=TRUE WHERE id=2", 1))
		referenceRunStep(t, p.ctx, tx, b.recorder, partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 4, 5, 6))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		// A retained RR snapshot must not be rejected by a newer committed root.
		tx, err = b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
		run(referenceExec("UPDATE t SET active=TRUE WHERE id=2", 1))
		referenceRunStep(t, p.ctx, tx, b.recorder, partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 4, 5))
	})
}
