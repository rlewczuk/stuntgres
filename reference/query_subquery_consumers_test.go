package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryConsumers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				run := func(s referenceStep) { s.prepared = prepared; raw(s) }
				raw(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				raw(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				raw(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
				raw(referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2))
				run(referenceExec("UPDATE t x SET v=(SELECT s.n FROM s WHERE s.k=x.id)", 3))
				raw(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), nil}}))
				step := referenceSelect("UPDATE t x SET v=v+1 WHERE EXISTS(SELECT 1 FROM s WHERE s.k=x.id) RETURNING id,(SELECT s.n FROM s WHERE s.k=x.id) AS scalar", []string{"id", "scalar"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}})
				step.bag = true
				run(step)
				// Actual DB plain Exec discards scalar RETURNING but retains count/state.
				plain := referenceExec("UPDATE t SET v=7 RETURNING (SELECT 7)", 3)
				plain.nonContext, plain.prepared = true, prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, plain)
				raw(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}}))
				raw(referenceExec("INSERT INTO s VALUES(1,303)", 1))
				for _, q := range []string{
					"UPDATE t x SET v=(SELECT n FROM s WHERE k=x.id)",
					"UPDATE t SET v=99 WHERE 1=(SELECT k FROM s WHERE k=1)",
					"UPDATE t SET v=99 RETURNING (SELECT n FROM s WHERE k=1)",
					"DELETE FROM t RETURNING (SELECT n FROM s WHERE k=1)",
				} {
					run(referenceErrorDiagnostic(q, subqueryCardinality()))
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						subqueryQueryFailure(t, p, b, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, q, subqueryCardinality())
						stmt.Close()
					} else {
						subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
					}
					raw(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}}))
				}
				// Self-read must observe predecessor values, not earlier staged writes.
				run(referenceExec("UPDATE t x SET v=(SELECT count(*) FROM t y WHERE y.v=7)", 3))
				raw(referenceSelect("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(3)}}))
				raw(referenceExec("CREATE TABLE inserted(v INT)", 0))
				run(referenceExec("INSERT INTO inserted VALUES((SELECT n FROM s WHERE k=2))", 1))
				run(referenceExec("INSERT INTO inserted SELECT (SELECT $1+v FROM t WHERE id=1)", 1, int64(10)))
				raw(referenceSelect("SELECT v FROM inserted ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(13)}, {int64(202)}}))
				run(referenceSelect("DELETE FROM t WHERE id=2 RETURNING id,(SELECT n FROM s WHERE k=2) AS scalar", []string{"id", "scalar"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(202)}}))
				plain = referenceExec("DELETE FROM t WHERE id=3 RETURNING (SELECT 7)", 1)
				plain.nonContext, plain.prepared = true, prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, plain)
				raw(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
				want := controlsReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "x"`)
				want.Position = "33"
				want.Detail = `There is an entry for table "x", but it cannot be referenced from this part of the query.`
				subqueryAnalysisFailure(raw, "UPDATE t x SET v=1 FROM (SELECT x.id) q", want)
			})
		})
	}
}

func TestReferenceQuerySubquerySemanticExclusions(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, tc := range []struct{ q, message, position string }{
			{"CREATE TABLE bad (i integer DEFAULT (SELECT 1))", "cannot use subquery in DEFAULT expression", "37"},
			{"CREATE TABLE t(a INT CHECK(EXISTS(SELECT 1)))", "cannot use subquery in check constraint", "28"},
		} {
			want := controlsReferenceDiagnostic("0A000", tc.message)
			want.Position = tc.position
			subqueryBoth(run, referenceErrorDiagnostic(tc.q, want))
		}
		for _, name := range []string{"bad", "t"} {
			want := controlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
			want.Position = "15"
			run(referenceErrorDiagnostic("SELECT * FROM "+name, want))
		}
	})
}

func TestReferenceQuerySubqueryReturningFrames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				run := func(step referenceStep) { step.prepared = prepared; raw(step) }
				raw(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				raw(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				run(referenceSelect("UPDATE t SET v=20 RETURNING (SELECT old.v) AS before,(SELECT new.v) AS after,ROW(1,10)=(SELECT old.*) AS old_row,ROW(1,20)=(SELECT new.*) AS new_row", []string{"before", "after", "old_row", "new_row"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{{int64(10), int64(20), true, true}}))
				run(referenceSelect("UPDATE t SET v=30 RETURNING WITH(OLD AS previous,NEW AS replacement) (SELECT previous.v) AS before,(SELECT replacement.v) AS after", []string{"before", "after"}, []string{"INT4", "INT4"}, [][]any{{int64(20), int64(30)}}))
				run(referenceSelect("DELETE FROM t RETURNING ROW(1,30)=(SELECT old.*) AS old_row,(SELECT new.v) AS absent", []string{"old_row", "absent"}, []string{"BOOL", "INT4"}, [][]any{{true, nil}}))
				raw(referenceSelect("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
			})
		})
	}
}
