package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromOuterReturning(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("CREATE TABLE s(k INT NOT NULL,n INT NOT NULL)", 0))
				run(referenceExec("CREATE TABLE r(k INT NOT NULL,z INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				run(referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2))
				run(referenceExec("INSERT INTO r VALUES(1,9),(3,30)", 2))
				q := "UPDATE t x SET v=r.z FROM s FULL JOIN r USING(k) WHERE x.id=k AND $1::bool RETURNING x.id,old.v,new.v,s.k,r.k,k,s.n,r.z"
				columns := []string{"id", "v", "v", "k", "k", "k", "n", "z"}
				types := []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}
				var executor referenceExecutor = c
				if prepared {
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				}
				first := referenceSelect(q, columns, types, [][]any{{int64(1), int64(10), int64(9), int64(1), int64(1), int64(1), int64(101), int64(9)}, {int64(2), int64(20), nil, int64(2), nil, int64(2), int64(202), nil}, {int64(3), int64(30), int64(30), nil, int64(3), int64(3), nil, int64(30)}}, true)
				first.bag = true
				referenceRunStep(t, p.ctx, executor, b.recorder, first)
				referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(q, columns, types, nil, false))
				repeat := referenceSelect(q, columns, types, [][]any{{int64(1), int64(9), int64(9), int64(1), int64(1), int64(1), int64(101), int64(9)}, {int64(2), nil, nil, int64(2), nil, int64(2), int64(202), nil}, {int64(3), int64(30), int64(30), nil, int64(3), int64(3), nil, int64(30)}}, true)
				repeat.bag = true
				referenceRunStep(t, p.ctx, executor, b.recorder, repeat)
				stars := referenceSelect("UPDATE t x SET v=r.z FROM s FULL JOIN r USING(k) WHERE x.id=k RETURNING x.*,s.*,r.*", []string{"id", "v", "k", "n", "k", "z"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(1), int64(101), int64(1), int64(9)}, {int64(2), nil, int64(2), int64(202), nil, nil}, {int64(3), int64(30), nil, nil, int64(3), int64(30)}})
				stars.bag = true
				if prepared {
					stars = referencePrepared(stars)
				}
				run(stars)
				run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}}))
			})
		})
	}
}

func TestReferenceUpdateFromOuterNotNullAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT NOT NULL,v INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				run(referenceExec("CREATE TABLE s(k INT NOT NULL,n INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2))
				run(referenceExec("CREATE TABLE r(k INT NOT NULL,z INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO r VALUES(1,9)", 1))
				q := "UPDATE t x SET v=r.z FROM s LEFT JOIN r USING(k) WHERE x.id=s.k RETURNING x.id"
				want := controlsReferenceDiagnostic("23502", `null value in column "v" of relation "t" violates not-null constraint`)
				want.Detail = "Failing row contains (2, null)."
				want.Schema = "public"
				want.Table = "t"
				want.Column = "v"
				var executor referenceExecutor = c
				if prepared {
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal("semantic Prepare must retain runtime constraint phase", err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				}
				result, err := executor.ExecContext(p.ctx, q)
				if result != nil || err == nil {
					t.Fatal("NULL-extended assignment exposed result or succeeded", result, err)
				}
				referenceAssertDiagnostic(t, err, &want)
				run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}

func TestReferenceUpdateFromOuterTargetDiagnostics(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, prepared := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/exec", true: "/prepare"}[prepared], func(t *testing.T) {
				updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
					run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
					run(referenceExec("CREATE TABLE r(k INT,z INT)", 0))
					q := "/*é*/ UPDATE t x SET v=s.n FROM s " + kind + " JOIN r ON x.id=s.k"
					want := controlsReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "x"`)
					want.Position = updateFromPosition(q, "x.id")
					want.Detail = `There is an entry for table "x", but it cannot be referenced from this part of the query.`
					updateFromDiagnostic(t, p.ctx, c, q, want, prepared)
					run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				})
			})
		}
	}
}

func TestReferenceUpdateFromOuterExecutionLegality(t *testing.T) {
	for _, stored := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "physical", true: "stored"}[stored]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2), referenceExec("CREATE TABLE s(k INT,n INT)", 0), referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2), referenceExec("CREATE TABLE r(k INT,z INT)", 0), referenceExec("INSERT INTO r VALUES(1,9),(3,30)", 2)} {
						run(step)
					}
					// Preserve both unmatched directions: a left-only strict WHERE
					// could let PostgreSQL reduce FULL to a legal LEFT join.
					q := "UPDATE t x SET v=7 FROM s FULL JOIN r ON s.k<r.k WHERE x.id=coalesce(s.k,r.k) RETURNING x.id"
					if stored {
						run(referenceExec("CREATE VIEW bad_join AS SELECT coalesce(s.k,r.k) AS k,s.n,r.z FROM s FULL JOIN r ON s.k<r.k", 0))
						q = "UPDATE t x SET v=7 FROM bad_join WHERE x.id=bad_join.k RETURNING x.id"
					}
					var executor referenceExecutor = c
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal("FULL condition restriction belongs to Execute", err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
					}
					result, err := executor.ExecContext(p.ctx, q)
					if result != nil || err == nil {
						t.Fatal("illegal FULL condition exposed result or succeeded", result, err)
					}
					want := controlsReferenceDiagnostic("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
					referenceAssertDiagnostic(t, err, &want)
					run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				})
			})
		}
	}
}
