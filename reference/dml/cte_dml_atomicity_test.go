package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLAtomicity(t *testing.T) {
	for _, kind := range []string{"main", "update", "delete", "merge", "cast", "not_null", "unique"} {
		for _, queryRows := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/exec", true: "/query"}[queryRows], func(t *testing.T) {
				cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := func(s fixture.Step) { cteDMLRun(t, p, b, false, s) }
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE audit(id INT)", 0), fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT NOT NULL,bad TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10,'bad'),(2,20,'2')", 2)} {
						run(s)
					}
					prefix := "WITH a AS (INSERT INTO audit VALUES(1) RETURNING id) "
					q := prefix + "SELECT 1/(id-id) FROM a"
					want := fixture.Diagnostic{Code: "22012", Message: "division by zero"}
					switch kind {
					case "update":
						q = prefix + "UPDATE t SET v=t.v+1 FROM a WHERE t.id=a.id RETURNING 1/(t.id-t.id)"
					case "delete":
						q = prefix + "DELETE FROM t USING a WHERE t.id=a.id RETURNING 1/(t.id-t.id)"
					case "merge":
						q = prefix + "MERGE INTO t USING a ON t.id=a.id WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING 1/(t.id-t.id)"
					case "cast":
						q = prefix + "UPDATE t SET v=bad::int4 FROM a WHERE t.id=a.id RETURNING t.id"
						want = fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}
					case "not_null":
						q = prefix + "UPDATE t SET v=NULL FROM a WHERE t.id=a.id RETURNING t.id"
						want = fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, null, bad).", Schema: "public", Table: "t", Column: "v"}
					case "unique":
						q = "WITH a AS (INSERT INTO audit VALUES(1) RETURNING id), first AS (INSERT INTO t SELECT id+2,30,'ok' FROM a RETURNING id) INSERT INTO t SELECT id,40,'bad' FROM first RETURNING id"
						want = fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(3) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"}
					}
					s := fixture.ImplicitError(q, want)
					s.SelectRows, s.ErrorStage = queryRows, "execute"
					cteDMLRun(t, p, b, prepared, s)
					run(fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{}))
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "bad"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(10), "bad"}, {int64(2), int64(20), "2"}}))
				})
			})
		}
	}
	for _, savepoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "transaction", true: "savepoint"}[savepoint], func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE audit(id INT)", 0))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "INSERT INTO audit VALUES(9)", 1)
				if savepoint {
					fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
				}
				q := "WITH a AS (INSERT INTO audit VALUES(1) RETURNING id), u AS (UPDATE t SET v=t.v+1 FROM a WHERE t.id=a.id RETURNING t.id) SELECT 1/(id-id) FROM u"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{}))
				if savepoint {
					fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO SAVEPOINT kept", 0)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
				} else {
					s = fixture.ImplicitError("SELECT id FROM audit", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
					s.SelectRows = true
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM audit", []string{"id"}, []string{"INT4"}, [][]any{}))
				}
				cteDMLRun(t, p, b, false, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}
