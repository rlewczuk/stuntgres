package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingMigratedExpressions(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.db)
		run(referenceExec("CREATE TABLE t(id INT,v INT,f BOOL)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE)", 3))
		q := "DELETE FROM t AS d WHERE d.v IS NOT DISTINCT FROM $1"
		stmt, err := b.db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		first := referenceExec(q, 1, nil)
		first.nonContext = true
		referenceRunStep(t, p.ctx, deleteUsingNonContextHeld{referenceHeldStatement: referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, direct: b.db}, b.recorder, first)
		decisive := "DELETE FROM t WHERE FALSE AND int4($1::text)=id"
		if b.pg {
			run(referenceExec(decisive, 0, "bad"))
		} else {
			run(referenceErrorDiagnostic(decisive, controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), "bad"))
		}
		for _, tc := range []struct{ q, token, message string }{
			{"DELETE FROM t d WHERE d.v::int4=10", "::", `syntax error at or near "::"`},
			{"DELETE FROM t d WHERE CAST(d.v AS int4)=10", ".", `syntax error at or near "."`},
			{"DELETE FROM t d WHERE int4(d.v)=10", ".", `syntax error at or near "."`},
		} {
			// Qualified no-USING casts retain the documented native boundary;
			// PostgreSQL executes the same SQL successfully inside a rollback.
			tx, err := b.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			boundaryRun := func(step referenceStep) { step.nonContext = true; referenceRunStep(t, p.ctx, tx, b.recorder, step) }
			if b.pg {
				boundaryRun(referenceExec(tc.q, 1))
			} else {
				want := controlsReferenceDiagnostic("42601", tc.message)
				want.Position = updateFromPosition(tc.q, tc.token)
				boundaryRun(referenceErrorDiagnostic(tc.q, want))
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceExec("DELETE FROM t AS d WHERE d.f IS FALSE AND CAST(v AS BIGINT)=20", 1))
		run(referenceExec("DELETE FROM t AS d WHERE d.v ISNULL", 0))
		run(referenceExec("DELETE FROM t AS d WHERE d.f IS TRUE AND v::int8=99", 0))
		run(referenceExec("DELETE FROM t WHERE int8(v)=99", 0))
		run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("CREATE TABLE frames(v INT)", 0))
		run(referenceExec("INSERT INTO frames VALUES(10),(NULL),(20)", 3))
		q = "DELETE FROM frames WHERE v IS NOT DISTINCT FROM int4($1::text)"
		held, err := b.db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := deleteUsingNonContextHeld{referenceHeldStatement: referenceHeldStatement{referenceExecutor: b.db, statement: held, query: q}, direct: b.db}
		for _, call := range []struct {
			arg       any
			bad       bool
			survivors [][]any
		}{
			{"bad", true, [][]any{{int64(10)}, {nil}, {int64(20)}}},
			{"10", false, [][]any{{nil}, {int64(20)}}},
			{nil, false, [][]any{{int64(20)}}},
			{"bad", true, [][]any{{int64(20)}}},
			{"20", false, nil},
		} {
			step := referenceExec(q, 1, call.arg)
			if call.bad {
				step = referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), call.arg)
			}
			step.nonContext = true
			referenceRunStep(t, p.ctx, executor, b.recorder, step)
			rows := referenceSelect("SELECT v FROM frames", []string{"v"}, []string{"INT4"}, call.survivors)
			rows.bag = true
			run(rows)
		}
	})
}
