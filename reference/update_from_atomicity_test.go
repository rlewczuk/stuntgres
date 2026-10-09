package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromPreflight(t *testing.T) {
	for _, clause := range []string{"SET v=$1::text::int FROM s WHERE FALSE", "SET v=1 FROM s JOIN r ON $1::text::int=r.k WHERE FALSE", "SET v=1 FROM s WHERE $1::text::int=0", "SET v=1 FROM s WHERE FALSE RETURNING $1::text::int"} {
		t.Run(clause, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				run(referenceExec("CREATE TABLE r(k INT)", 0))
				run(referenceExec("DELETE FROM s", 4))
				q := "UPDATE t x " + clause
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				for _, arg := range []any{"bad", "1", nil, "bad", "2"} {
					var step referenceStep
					if arg == "bad" {
						step = referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22P02", "invalid input syntax for type integer: \"bad\""), arg)
					} else {
						step = referenceExec(q, 0, arg)
					}
					referenceRunStep(t, p.ctx, held, b.recorder, step)
				}
				run(updateFromTargets(updateFromOriginal()))
				run(referenceExec("UPDATE t x SET v=s.n::text::int FROM s WHERE FALSE", 0))
			})
		})
	}
}

func TestReferenceUpdateFromAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, ddl, values, q, code, message, detail, column, constraint string }{
		{"not_null", "CREATE TABLE t(id INT,v INT NOT NULL)", "(1,101),(2,NULL)", "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", "23502", "null value in column \"v\" of relation \"t\" violates not-null constraint", "Failing row contains (2, null).", "v", ""},
		{"check", "CREATE TABLE t(id INT,v INT CHECK(v<200))", "(1,101),(2,202)", "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", "23514", "new row for relation \"t\" violates check constraint \"t_v_check\"", "Failing row contains (2, 202).", "", "t_v_check"},
		{"division", "CREATE TABLE t(id INT,v INT)", "(1,1),(2,0)", "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k RETURNING 10/s.n", "22012", "division by zero", "", "", ""},
		{"cast", "CREATE TABLE t(id INT,v INT)", "(1,1),(2,0)", "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k RETURNING (s.n::text || 'bad')::int", "22P02", "invalid input syntax for type integer: \"1bad\"", "", "", ""},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec(tc.ddl, 0))
					run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(referenceExec("INSERT INTO s VALUES"+tc.values, 2))
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail = tc.detail
					want.Column = tc.column
					want.Constraint = tc.constraint
					if tc.column != "" || tc.constraint != "" {
						want.Schema = "public"
						want.Table = "t"
					}
					step := referenceErrorDiagnostic(tc.q, want)
					if prepared {
						step = referencePrepared(step)
					}
					run(step)
					original := referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
					run(original)
					run(referenceExec("BEGIN", 0))
					run(referenceExec("UPDATE t SET v=11 WHERE id=1", 1))
					run(referenceExec("SAVEPOINT before_from", 0))
					run(step)
					run(referenceErrorDiagnostic("SELECT id FROM t", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(referenceExec("ROLLBACK TO before_from", 0))
					run(referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(referenceExec("ROLLBACK", 0))
					run(original)
				})
			})
		}
	}
}
