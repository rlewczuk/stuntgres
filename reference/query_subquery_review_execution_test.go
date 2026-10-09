package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

// Keep the same statement alive across source changes; Prepare itself must not
// execute the failing projection. The common runner owns row/metadata recording.
func subqueryReviewHold(t *testing.T, p *referencePair, c *sql.Conn, q string) referenceHeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal("Prepare must succeed", err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
}

func subqueryReviewFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, want referenceDiagnostic) {
	t.Helper()
	// Query can report a PostgreSQL streaming error; native cannot publish a
	// cursor. Exec must publish neither a result nor an affected-row count.
	subqueryQueryFailure(t, p, b, x, q, want)
	s := referenceErrorDiagnostic(q, want)
	s.errorStage = "execute"
	referenceRunStep(t, p.ctx, x, b.recorder, s)
}

func TestReferenceQuerySubquerySecondTupleProjection(t *testing.T) {
	for _, tc := range []struct {
		name, query, column, typ string
		value                    any
	}{
		{"scalar", "SELECT(SELECT CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t ORDER BY t.id) AS s", "s", "INT4", int64(1)},
		{"row", "SELECT ROW(1,1)=(SELECT id,CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t ORDER BY t.id) AS matched", "matched", "BOOL", true},
		{"scalar-unsorted", "SELECT(SELECT CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t) AS s", "s", "INT4", int64(1)},
		{"row-unsorted", "SELECT ROW(1,1)=(SELECT id,CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t) AS matched", "matched", "BOOL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				held := subqueryReviewHold(t, p, c, tc.query)
				check := func(value any) {
					s := referenceSelect(tc.query, []string{tc.column}, []string{tc.typ}, [][]any{{value}})
					run(s)
					referenceRunStep(t, p.ctx, held, b.recorder, s)
				}
				check(tc.value)
				run(referenceExec("INSERT INTO t VALUES(2)", 1))
				division := controlsReferenceDiagnostic("22012", "division by zero")
				subqueryReviewFailure(t, p, b, c, tc.query, division)
				subqueryReviewFailure(t, p, b, held, tc.query, division)
				run(referenceExec("DELETE FROM t WHERE id=2", 1))
				check(tc.value)
				run(referenceExec("DELETE FROM t", 1))
				check(nil)
			})
		})
	}
	t.Run("second-not-third", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
			// This unsorted input needs no upstream materialization. After the
			// second valid tuple, cardinality must win without a third probe.
			for _, q := range []string{
				"SELECT(SELECT CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t) AS s",
				"SELECT ROW(1,1)=(SELECT id,CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t) AS matched",
				"SELECT(SELECT 1 FROM t) AS s",
			} {
				held := subqueryReviewHold(t, p, c, q)
				subqueryReviewFailure(t, p, b, c, q, subqueryCardinality())
				subqueryReviewFailure(t, p, b, held, q, subqueryCardinality())
			}
		})
	})
	t.Run("ordered-third-projection", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
			// PostgreSQL's sort input needs the projection before producing any
			// subplan tuple. This is not a third cardinality-probe tuple.
			for _, q := range []string{
				"SELECT(SELECT CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t ORDER BY t.id) AS s",
				"SELECT ROW(1,1)=(SELECT id,CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t ORDER BY t.id) AS matched",
			} {
				held := subqueryReviewHold(t, p, c, q)
				want := controlsReferenceDiagnostic("22012", "division by zero")
				subqueryReviewFailure(t, p, b, c, q, want)
				subqueryReviewFailure(t, p, b, held, q, want)
			}
		})
	})
	t.Run("update-atomicity", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			run(referenceExec("CREATE TABLE a(id INT,label TEXT)", 0))
			run(referenceExec("INSERT INTO a VALUES(1,'a'),(2,'b')", 2))
			q := "UPDATE a SET id=(SELECT CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t ORDER BY id) WHERE id=1 RETURNING id"
			held := subqueryReviewHold(t, p, c, q)
			for _, x := range []referenceExecutor{c, held} {
				subqueryReviewFailure(t, p, b, x, q, controlsReferenceDiagnostic("22012", "division by zero"))
				subqueryBoth(run, referenceSelect("SELECT id,label FROM a ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}}))
			}
		})
	})
}

func TestReferenceQuerySubqueryPlainGroupDemand(t *testing.T) {
	for _, tc := range []struct {
		name, child      string
		present, failure bool
	}{
		{"ignored-target", "SELECT(SELECT v FROM demand_bad) FROM demand_outer GROUP BY id", true, false},
		{"empty-filter", "SELECT(SELECT v FROM demand_bad) FROM demand_outer WHERE FALSE GROUP BY id", false, false},
		{"ignored-key", "SELECT 1 FROM demand_outer GROUP BY CASE WHEN id=2 THEN 1/(id-2) ELSE id END", true, false},
		{"offset-zero", "SELECT(SELECT v FROM demand_bad) FROM demand_outer GROUP BY id OFFSET 0", false, true},
		{"local-aggregate", "SELECT(SELECT v FROM demand_bad),count(*) FROM demand_outer", false, true},
		{"having-true", "SELECT(SELECT v FROM demand_bad) FROM demand_outer HAVING TRUE", false, true},
		{"having-false", "SELECT(SELECT v FROM demand_bad) FROM demand_outer HAVING FALSE", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(referenceExec("CREATE TABLE demand_outer(id INT)", 0))
				run(referenceExec("INSERT INTO demand_outer VALUES(1),(2)", 2))
				run(referenceExec("CREATE TABLE demand_bad(v INT)", 0))
				run(referenceExec("INSERT INTO demand_bad VALUES(1),(2)", 2))
				q := "SELECT EXISTS(" + tc.child + ") AS present"
				held := subqueryReviewHold(t, p, c, q)
				check := func(present bool) {
					s := referenceSelect(q, []string{"present"}, []string{"BOOL"}, [][]any{{present}})
					run(s)
					referenceRunStep(t, p.ctx, held, b.recorder, s)
				}
				if tc.failure {
					subqueryReviewFailure(t, p, b, c, q, subqueryCardinality())
					subqueryReviewFailure(t, p, b, held, q, subqueryCardinality())
					return
				}
				check(tc.present)
				run(referenceExec("DELETE FROM demand_outer", 2))
				check(false)
				run(referenceExec("INSERT INTO demand_outer VALUES(1),(2)", 2))
				check(tc.present)
			})
		})
	}
	t.Run("stored-original-graph", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(referenceExec("CREATE TABLE demand_outer(id INT)", 0))
			run(referenceExec("INSERT INTO demand_outer VALUES(1),(2)", 2))
			run(referenceExec("CREATE TABLE demand_bad(v INT)", 0))
			run(referenceExec("INSERT INTO demand_bad VALUES(1),(2)", 2))
			run(referenceExec("CREATE VIEW grouping_v AS SELECT EXISTS(SELECT(SELECT v FROM demand_bad) FROM demand_outer GROUP BY id) AS present", 0))
			dependency := controlsReferenceDiagnostic("2BP01", "cannot drop column v of table demand_bad because other objects depend on it")
			dependency.Detail = "view grouping_v depends on column v of table demand_bad"
			dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			drop := "ALTER TABLE demand_bad DROP COLUMN v"
			dropHeld := subqueryReviewHold(t, p, c, drop)
			subqueryReviewFailure(t, p, b, c, drop, dependency)
			subqueryReviewFailure(t, p, b, dropHeld, drop, dependency)
			run(referenceExec("CREATE VIEW grouping_key_v AS SELECT EXISTS(SELECT 1 FROM demand_outer GROUP BY CASE WHEN id=2 THEN 1/(id-2) ELSE id END) AS key_present", 0))
			referenceViewCopy(t, p, b, "grouping_v", "grouping_copy")
			referenceViewCopy(t, p, b, "grouping_key_v", "grouping_key_copy")
			check := func() {
				for _, view := range []string{"grouping_v", "grouping_copy"} {
					subqueryBoth(run, referenceSelect("SELECT * FROM "+view, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
				}
				for _, view := range []string{"grouping_key_v", "grouping_key_copy"} {
					subqueryBoth(run, referenceSelect("SELECT * FROM "+view, []string{"key_present"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			check()
			run(referenceExec("ALTER TABLE demand_bad RENAME TO demand_renamed", 0))
			run(referenceExec("ALTER TABLE demand_renamed RENAME COLUMN v TO renamed_v", 0))
			check()
			if !b.pg {
				dump := referenceViewDump(t, p)
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				referenceViewRestore(t, p, dump)
				check()
			}
			// CASCADE is a positive dependency witness, not an invented error
			// record: both ignored-target views must depend on the original v.
			run(referenceExec("ALTER TABLE demand_renamed DROP COLUMN renamed_v CASCADE", 0))
			subqueryBoth(run, referenceSelect("SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_views WHERE viewname='grouping_v' OR viewname='grouping_copy') AS retained", []string{"retained"}, []string{"BOOL"}, [][]any{{false}}))
			subqueryBoth(run, referenceSelect("SELECT * FROM grouping_key_v", []string{"key_present"}, []string{"BOOL"}, [][]any{{true}}))
		})
	})
}

func TestReferenceQuerySubqueryDistinctComposition(t *testing.T) {
	for _, tc := range []struct {
		name, query string
	}{
		{"direct", "SELECT DISTINCT(SELECT t.id WHERE t.id=1) AS scalar FROM t ORDER BY scalar"},
		{"derived", "SELECT d.scalar FROM (SELECT DISTINCT(SELECT t.id WHERE t.id=1) AS scalar FROM t) d ORDER BY d.scalar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
				held := subqueryReviewHold(t, p, c, tc.query)
				check := func(rows [][]any) {
					s := referenceSelect(tc.query, []string{"scalar"}, []string{"INT4"}, rows)
					run(s)
					referenceRunStep(t, p.ctx, held, b.recorder, s)
				}
				check([][]any{{int64(1)}, {nil}})
				run(referenceExec("DELETE FROM t WHERE id=1", 1))
				check([][]any{{nil}})
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				check([][]any{{int64(1)}, {nil}})
				run(referenceExec("CREATE VIEW distinct_v(result) AS "+tc.query, 0))
				referenceViewCopy(t, p, b, "distinct_v", "distinct_copy")
				storedCheck := func() {
					for _, view := range []string{"distinct_v", "distinct_copy"} {
						subqueryBoth(run, referenceSelect("SELECT * FROM "+view+" ORDER BY result", []string{"result"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
					}
				}
				storedCheck()
				if !b.pg {
					dump := referenceViewDump(t, p)
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					referenceViewRestore(t, p, dump)
					storedCheck()
				}
			})
		})
	}
	t.Run("ignored-distinct-target", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(referenceExec("CREATE TABLE demand_outer(id INT)", 0))
			run(referenceExec("INSERT INTO demand_outer VALUES(1),(2)", 2))
			run(referenceExec("CREATE TABLE demand_bad(v INT)", 0))
			run(referenceExec("INSERT INTO demand_bad VALUES(1),(2)", 2))
			q := "SELECT EXISTS(SELECT DISTINCT(SELECT v FROM demand_bad) FROM demand_outer) AS present"
			held := subqueryReviewHold(t, p, c, q)
			s := referenceSelect(q, []string{"present"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			run(referenceExec("CREATE VIEW distinct_ignored_v AS "+q, 0))
			referenceViewCopy(t, p, b, "distinct_ignored_v", "distinct_ignored_copy")
			check := func() {
				for _, view := range []string{"distinct_ignored_v", "distinct_ignored_copy"} {
					subqueryBoth(run, referenceSelect("SELECT * FROM "+view, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			check()
			if !b.pg {
				dump := referenceViewDump(t, p)
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				referenceViewRestore(t, p, dump)
				check()
			}
		})
	})
}
