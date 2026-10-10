package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingOuterSources(t *testing.T) {
	for _, join := range []string{"LEFT", "RIGHT", "FULL"} {
		t.Run(join, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				run(referenceExec("CREATE TABLE s(k INT)", 0))
				run(referenceExec("CREATE TABLE r(k BIGINT)", 0))
				run(referenceExec("INSERT INTO s VALUES(1),(2)", 2))
				run(referenceExec("INSERT INTO r VALUES(2),(3)", 2))
				q := "DELETE FROM t d USING s " + join + " JOIN r USING(k) AS u WHERE d.id=u.k RETURNING d.id,u.k,s.k,r.k"
				want := [][]any{{int64(2), int64(2), int64(2), int64(2)}}
				var survivors [][]any
				if join != "RIGHT" {
					want = append(want, []any{int64(1), int64(1), int64(1), nil})
				} else {
					survivors = append(survivors, []any{int64(1), int64(10)})
				}
				if join != "LEFT" {
					want = append(want, []any{int64(3), int64(3), nil, int64(3)})
				} else {
					survivors = append(survivors, []any{int64(3), int64(30)})
				}
				step := referenceSelect(q, []string{"id", "k", "k", "k"}, []string{"INT4", "INT8", "INT4", "INT8"}, want)
				step.bag = true
				run(step)
				run(deleteUsingTargets(survivors))
				run(referenceSelect("SELECT k FROM s ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(referenceSelect("SELECT k FROM r ORDER BY k", []string{"k"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(3)}}))
			})
		})
	}
}

func TestReferenceDeleteUsingSourceProducers(t *testing.T) {
	for _, tc := range []struct {
		name, setup, source, predicate string
		count                          int64
	}{
		{"cross", "CREATE VIEW selected AS SELECT k FROM s WHERE k=2", "selected CROSS JOIN (SELECT 1 AS n) one", "d.id=selected.k", 2},
		{"natural", "CREATE TABLE r(k INT)", "s NATURAL JOIN r", "d.id=s.k", 0},
		{"sequence", "CREATE SEQUENCE seq START 2 CACHE 1", "seq", "d.id=seq.last_value", 0},
		{"catalog", "CREATE VIEW selected AS SELECT k FROM s", "pg_catalog.pg_class catalog", "catalog.relname='t'", 6},
		{"column_alias", "CREATE VIEW selected AS SELECT k,label FROM s", "selected AS x(key,txt)", "d.id=x.key", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				deleteUsingSeed(run)
				run(referenceExec(tc.setup, 0))
				q := "DELETE FROM t d USING " + tc.source + " WHERE " + tc.predicate
				if tc.name == "sequence" {
					// DELETE reference rowmarks reject a sequence source without
					// demanding a sequence value or changing either relation.
					want := controlsReferenceDiagnostic("42809", `cannot lock rows in sequence "seq"`)
					for _, prepared := range []bool{false, true} {
						step := referenceErrorDiagnostic(q, want)
						step.errorStage = "execute"
						if prepared {
							stmt, err := c.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal("sequence USING must prepare successfully", err)
							}
							defer stmt.Close()
							run(deleteUsingTargets(deleteUsingOriginal()))
							deleteUsingSourcesUnchanged(run)
							run(referenceSelect("SELECT last_value,is_called FROM seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), false}}))
							held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
							referenceRunStep(t, p.ctx, held, b.recorder, step)
						} else {
							run(step)
						}
						run(deleteUsingTargets(deleteUsingOriginal()))
						deleteUsingSourcesUnchanged(run)
						run(referenceSelect("SELECT last_value,is_called FROM seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), false}}))
					}
				} else {
					run(referenceExec(q, tc.count))
				}
				want := deleteUsingOriginal()
				if tc.count == 2 {
					want = append(deleteUsingSurvivors(), []any{int64(3), int64(30)})
				} else if tc.count == 3 {
					want = deleteUsingSurvivors()
				} else if tc.count == 6 {
					want = nil
				}
				run(deleteUsingTargets(want))
				deleteUsingSourcesUnchanged(run)
			})
		})
	}
}

func TestReferenceDeleteUsingZeroWidth(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(referenceExec("CREATE TABLE empty_columns()", 0))
		run(referenceExec("INSERT INTO empty_columns DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO empty_columns DEFAULT VALUES", 1))
		run(referenceExec("DELETE FROM t USING empty_columns", 6))
		run(deleteUsingTargets(nil))
		run(referenceSelect("SELECT COUNT(*) FROM empty_columns", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		deleteUsingSourcesUnchanged(run)
	})
}

func TestReferenceDeleteUsingCorrelatedExpressions(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		step := referenceSelect("DELETE FROM t d USING s x WHERE d.id=x.k AND (SELECT x.k)=d.id RETURNING d.id,(SELECT x.label)", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "two"}, {int64(2), "two"}, {int64(3), "three"}})
		step.bag = true
		run(step)
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
	})
}
