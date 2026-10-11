package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckAtomicity(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT DEFAULT 10,b INT DEFAULT 1)", 0), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE a<b WITH CHECK OPTION", 0))
		for _, q := range []string{"INSERT INTO v VALUES(1,2),(3,2)", "INSERT INTO v SELECT a,b FROM (VALUES(1,2),(3,2)) AS src(a,b) ORDER BY a", "INSERT INTO v DEFAULT VALUES", "INSERT INTO v VALUES(10)"} {
			values := "3, 2"
			if q == "INSERT INTO v DEFAULT VALUES" || q == "INSERT INTO v VALUES(10)" {
				values = "10, 1"
			}
			run(viewCheckError(q, "v", values), viewCheckPairRows())
		}
		run(fixture.Exec("INSERT INTO v VALUES(1,4),(2,3)", 2))
		s := viewCheckError("UPDATE v SET a=a+1 RETURNING a,b", "v", "3, 3")
		s.SelectRows = true
		run(s, viewCheckPairRows([]any{int64(1), int64(4)}, []any{int64(2), int64(3)}))
		late := fixture.ImplicitError("UPDATE v SET b=b+1 RETURNING 10/(a-2)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		late.SelectRows = true
		run(late, viewCheckPairRows([]any{int64(1), int64(4)}, []any{int64(2), int64(3)}))
		run(fixture.Exec("CREATE TABLE constrained(id INT PRIMARY KEY,a INT NOT NULL CONSTRAINT positive CHECK(a>0))", 0), fixture.Exec("CREATE VIEW cv AS SELECT id,a FROM constrained WHERE a>10 WITH CHECK OPTION", 0), fixture.Exec("INSERT INTO constrained VALUES(1,20)", 1))
		run(fixture.ImplicitError("INSERT INTO cv VALUES(2,NULL)", fixture.Diagnostic{Code: "23502", Message: "null value in column \"a\" of relation \"constrained\" violates not-null constraint", Detail: "Failing row contains (2, null).", Schema: "public", Table: "constrained", Column: "a"}), fixture.ImplicitError("INSERT INTO cv VALUES(2,-1)", fixture.Diagnostic{Code: "23514", Message: "new row for relation \"constrained\" violates check constraint \"positive\"", Detail: "Failing row contains (2, -1).", Schema: "public", Table: "constrained", Constraint: "positive"}), fixture.ImplicitError("INSERT INTO cv VALUES(1,1)", fixture.Diagnostic{Code: "23505", Message: "duplicate key value violates unique constraint \"constrained_pkey\"", Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "constrained", Constraint: "constrained_pkey"}), fixture.Exec("INSERT INTO cv VALUES(1,1) ON CONFLICT DO NOTHING", 0), viewCheckError("INSERT INTO cv VALUES(2,1) ON CONFLICT DO NOTHING", "cv", "2, 1"), fixture.Select("SELECT id,a FROM constrained", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}}))
		run(fixture.Exec("CREATE VIEW empty AS SELECT a,b FROM t WHERE a<b AND 10/(a-99)>0 WITH CHECK OPTION", 0), fixture.Select("UPDATE empty SET a=99 WHERE a=999 RETURNING *", []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
	})
}

func TestReferenceViewCheckSources(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE lookup(k INT)", 0), fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("INSERT INTO lookup VALUES(1),(2)", 2), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE EXISTS(SELECT 1 FROM lookup WHERE lookup.k=t.a) WITH CHECK OPTION", 0))
		q := "INSERT INTO v VALUES($1,$2)"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 1, 1, 10))
		run(fixture.Exec("DELETE FROM lookup WHERE k=2", 1))
		fixture.RunStep(t, p.Ctx, e, b.Recorder, viewCheckError(q, "v", "2, 20", 2, 20))
		run(viewCheckPairRows([]any{int64(1), int64(10)}), fixture.Select("SELECT k FROM lookup", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		own := func(s fixture.Step) { s.Prepared = held; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		own(fixture.Exec("INSERT INTO lookup VALUES(3)", 1))
		own(fixture.Exec("INSERT INTO v VALUES(3,30)", 1))
		own(fixture.Exec("UPDATE lookup SET k=4 WHERE k=3", 1))
		own(viewCheckError("INSERT INTO v VALUES(3,31)", "v", "3, 31"))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		tx, err = b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		own(fixture.Exec("INSERT INTO lookup VALUES(3)", 1))
		own(fixture.Exec("UPDATE lookup SET k=4 WHERE k=3", 1))
		own(fixture.Exec("INSERT INTO v VALUES(4,40)", 1))
		own(fixture.Exec("SAVEPOINT prior", 0))
		own(fixture.Exec("DELETE FROM lookup WHERE k=4", 1))
		own(viewCheckError("INSERT INTO v VALUES(4,41)", "v", "4, 41"))
		own(fixture.Exec("ROLLBACK TO prior", 0))
		own(fixture.Exec("UPDATE v SET b=42 WHERE a=4", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(4), int64(42)}), fixture.Select("SELECT k FROM lookup ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}))
		run(fixture.Exec("CREATE VIEW snapshot_v AS SELECT a,b FROM t WHERE NOT EXISTS(SELECT 1 FROM t AS old WHERE old.a=t.a) WITH CHECK OPTION", 0), fixture.Exec("INSERT INTO snapshot_v VALUES(5,50),(5,51)", 2), viewCheckError("INSERT INTO snapshot_v VALUES(5,52)", "snapshot_v", "5, 52"), viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(4), int64(42)}, []any{int64(5), int64(50)}, []any{int64(5), int64(51)}))
	})
}
