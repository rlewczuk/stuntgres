package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func partialIndexScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			body(t, p, b, run)
		})
	}
}

func partialIndexSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(id INT,k INT,active BOOL)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,10,TRUE),(2,20,FALSE),(3,30,NULL),(4,NULL,TRUE),(5,10,TRUE)", 5))
}

func TestReferencePartialIndexMembership(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				partialIndexSeed(run)
				for _, q := range []string{
					"CREATE INDEX pi ON t(k) WHERE active",
					"CREATE INDEX nulls ON t(k) WHERE active IS NULL",
					"CREATE INDEX all_rows ON t(k) WHERE TRUE",
					"CREATE INDEX no_rows ON t(k) WHERE FALSE",
					"CREATE INDEX unknown_rows ON t(k) WHERE NULL",
					"CREATE INDEX compound ON t((k+1) int4_ops,id DESC) INCLUDE(active) WHERE active AND k=10",
				} {
					s := fixture.Exec(q, 0)
					if held {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				for _, s := range []fixture.Step{
					fixture.PartialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE active IS NOT TRUE ORDER BY id", 2, 3),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE active IS NULL ORDER BY id", 3),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE active AND k=10 ORDER BY id", 1, 5),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE FALSE ORDER BY id"),
					fixture.PartialIndexIDs("SELECT id FROM t WHERE NULL ORDER BY id"),
					fixture.Select("SELECT id,k,active FROM t ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(10), true}, {int64(2), int64(20), false}, {int64(3), int64(30), nil}, {int64(4), nil, true}, {int64(5), int64(10), true}}),
				} {
					run(s)
					run(fixture.Prepared(s))
				}
			})
		})
	}
}

func TestReferencePartialIndexTransitions(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		partialIndexSeed(run)
		run(fixture.Exec("CREATE INDEX pi ON t(k) WHERE active", 0))
		run(fixture.Prepared(fixture.Exec("UPDATE t SET active=TRUE WHERE id=2 OR id=3", 2)))
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 3, 4, 5))
		run(fixture.Exec("UPDATE t SET active=FALSE WHERE id=1", 1))
		run(fixture.Exec("UPDATE t SET active=NULL WHERE id=4", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE t SET k=11 WHERE id=5", 1)))
		run(fixture.Exec("UPDATE t SET k=99 WHERE id=1", 1))
		run(fixture.Exec("INSERT INTO t VALUES(6,60,TRUE),(7,70,FALSE),(8,80,NULL)", 3))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t SELECT id+10,k,active FROM t WHERE id=6 OR id=7", 2)))
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 6, 16))
		run(fixture.Prepared(fixture.Exec("DELETE FROM t WHERE id=6 OR id=7", 2)))
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 16))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		trun(fixture.Exec("UPDATE t SET active=TRUE WHERE id=1", 1))
		trun(fixture.Exec("SAVEPOINT s", 0))
		trun(fixture.Exec("DELETE FROM t WHERE active", 5))
		trun(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id"))
		trun(fixture.Exec("ROLLBACK TO s", 0))
		trun(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 3, 5, 16))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 16))
		tx, err = b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("UPDATE t SET active=TRUE WHERE id=8", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 3, 5, 8, 16))
	})
}

func TestReferencePartialIndexPredicateFirst(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(id INT,n INT,active BOOL)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,0,FALSE),(2,2,TRUE),(3,0,NULL),(4,2,FALSE)", 4))
				create := fixture.Exec("CREATE INDEX pi ON t((10/n)) WHERE active", 0)
				if held {
					create = fixture.Prepared(create)
				}
				run(create)
				run(fixture.Exec("INSERT INTO t VALUES(5,0,FALSE),(6,2,TRUE)", 2))
				for _, q := range []string{"UPDATE t SET active=TRUE WHERE id=1 OR id=4", "INSERT INTO t VALUES(7,2,TRUE),(8,0,TRUE)"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.ErrorStage = "execute"
					if held {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 6))
					run(fixture.PartialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5, 6))
				}
				run(fixture.Exec("UPDATE t SET active=TRUE WHERE id=4", 1))
				run(fixture.Select("SELECT id,10/n AS key FROM t WHERE active ORDER BY id", []string{"id", "key"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(4), int64(5)}, {int64(6), int64(5)}}))
				run(fixture.ImplicitError("CREATE INDEX broken ON t(id) WHERE 10/n>0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='broken'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE TABLE predicates(id INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO predicates VALUES(1,2)", 1))
				run(fixture.Exec("CREATE INDEX pred ON predicates(id) WHERE 10/n>0", 0))
				run(fixture.ImplicitError("INSERT INTO predicates VALUES(2,2),(3,0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				run(fixture.PartialIndexIDs("SELECT id FROM predicates ORDER BY id", 1))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT s", 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("UPDATE predicates SET n=0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO s", 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT n FROM predicates", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestReferencePartialIndexParameters(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		partialIndexSeed(run)
		run(fixture.Exec("CREATE INDEX fixed ON t(k) WHERE k=10", 0))
		run(fixture.Exec("CREATE INDEX active_rows ON t(k) WHERE active", 0))
		for _, q := range []string{"SELECT id FROM t WHERE k=$1 ORDER BY id", "SELECT id FROM t WHERE active AND k=$1 ORDER BY id"} {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
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
				s := fixture.PartialIndexIDs(q, ids...)
				s.Args = []any{tc.arg}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
			}
		}
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active OR k=20 ORDER BY id", 1, 2, 4, 5))
		run(fixture.PartialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5))
		run(fixture.Exec("CREATE INDEX bound ON t(k) WHERE k<20", 0))
		q := "SELECT id FROM t WHERE k<$1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg int64
			ids []int64
		}{{20, []int64{1, 5}}, {40, []int64{1, 2, 3, 5}}, {10, nil}, {40, []int64{1, 2, 3, 5}}} {
			s := fixture.PartialIndexIDs(q, tc.ids...)
			s.Args = []any{tc.arg}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO t VALUES(6,10,TRUE)", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("UPDATE t SET active=TRUE WHERE id=2", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 2, 4, 5, 6))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		// A retained RR snapshot must not be rejected by a newer committed root.
		tx, err = b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		run(fixture.Exec("UPDATE t SET active=FALSE WHERE id=1", 1))
		run(fixture.Exec("UPDATE t SET active=TRUE WHERE id=2", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1, 4, 5))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.PartialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 2, 4, 5))
	})
}
