package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Keep the same statement alive across source changes; Prepare itself must not
// execute the failing projection. The common runner owns row/metadata recording.
func subqueryReviewHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("Prepare must succeed", err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
}

func subqueryReviewFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	// Query can report a PostgreSQL streaming error; native cannot publish a
	// cursor. Exec must publish neither a result nor an affected-row count.
	subqueryQueryFailure(t, p, b, x, q, want)
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
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
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				held := subqueryReviewHold(t, p, c, tc.query)
				check := func(value any) {
					s := fixture.Select(tc.query, []string{tc.column}, []string{tc.typ}, [][]any{{value}})
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.value)
				run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
				division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				subqueryReviewFailure(t, p, b, c, tc.query, division)
				subqueryReviewFailure(t, p, b, held, tc.query, division)
				run(fixture.Exec("DELETE FROM t WHERE id=2", 1))
				check(tc.value)
				run(fixture.Exec("DELETE FROM t", 1))
				check(nil)
			})
		})
	}
	t.Run("second-not-third", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2),(3)", 3))
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
		subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2),(3)", 3))
			// PostgreSQL's sort input needs the projection before producing any
			// subplan tuple. This is not a third cardinality-probe tuple.
			for _, q := range []string{
				"SELECT(SELECT CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t ORDER BY t.id) AS s",
				"SELECT ROW(1,1)=(SELECT id,CASE WHEN id=3 THEN 1/(id-3) ELSE id END FROM t ORDER BY t.id) AS matched",
			} {
				held := subqueryReviewHold(t, p, c, q)
				want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				subqueryReviewFailure(t, p, b, c, q, want)
				subqueryReviewFailure(t, p, b, held, q, want)
			}
		})
	})
	t.Run("update-atomicity", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			run(fixture.Exec("CREATE TABLE a(id INT,label TEXT)", 0))
			run(fixture.Exec("INSERT INTO a VALUES(1,'a'),(2,'b')", 2))
			q := "UPDATE a SET id=(SELECT CASE WHEN id=2 THEN 1/(id-2) ELSE id END FROM t ORDER BY id) WHERE id=1 RETURNING id"
			held := subqueryReviewHold(t, p, c, q)
			for _, x := range []fixture.Executor{c, held} {
				subqueryReviewFailure(t, p, b, x, q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				subqueryBoth(run, fixture.Select("SELECT id,label FROM a ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}}))
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
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE demand_outer(id INT)", 0))
				run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2)", 2))
				run(fixture.Exec("CREATE TABLE demand_bad(v INT)", 0))
				run(fixture.Exec("INSERT INTO demand_bad VALUES(1),(2)", 2))
				q := "SELECT EXISTS(" + tc.child + ") AS present"
				held := subqueryReviewHold(t, p, c, q)
				check := func(present bool) {
					s := fixture.Select(q, []string{"present"}, []string{"BOOL"}, [][]any{{present}})
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				if tc.failure {
					subqueryReviewFailure(t, p, b, c, q, subqueryCardinality())
					subqueryReviewFailure(t, p, b, held, q, subqueryCardinality())
					return
				}
				check(tc.present)
				run(fixture.Exec("DELETE FROM demand_outer", 2))
				check(false)
				run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2)", 2))
				check(tc.present)
			})
		})
	}
	t.Run("stored-original-graph", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE demand_outer(id INT)", 0))
			run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2)", 2))
			run(fixture.Exec("CREATE TABLE demand_bad(v INT)", 0))
			run(fixture.Exec("INSERT INTO demand_bad VALUES(1),(2)", 2))
			run(fixture.Exec("CREATE VIEW grouping_v AS SELECT EXISTS(SELECT(SELECT v FROM demand_bad) FROM demand_outer GROUP BY id) AS present", 0))
			dependency := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column v of table demand_bad because other objects depend on it")
			dependency.Detail = "view grouping_v depends on column v of table demand_bad"
			dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			drop := "ALTER TABLE demand_bad DROP COLUMN v"
			dropHeld := subqueryReviewHold(t, p, c, drop)
			subqueryReviewFailure(t, p, b, c, drop, dependency)
			subqueryReviewFailure(t, p, b, dropHeld, drop, dependency)
			run(fixture.Exec("CREATE VIEW grouping_key_v AS SELECT EXISTS(SELECT 1 FROM demand_outer GROUP BY CASE WHEN id=2 THEN 1/(id-2) ELSE id END) AS key_present", 0))
			fixture.ViewCopy(t, p, b, "grouping_v", "grouping_copy")
			fixture.ViewCopy(t, p, b, "grouping_key_v", "grouping_key_copy")
			check := func() {
				for _, view := range []string{"grouping_v", "grouping_copy"} {
					subqueryBoth(run, fixture.Select("SELECT * FROM "+view, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
				}
				for _, view := range []string{"grouping_key_v", "grouping_key_copy"} {
					subqueryBoth(run, fixture.Select("SELECT * FROM "+view, []string{"key_present"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			check()
			run(fixture.Exec("ALTER TABLE demand_bad RENAME TO demand_renamed", 0))
			run(fixture.Exec("ALTER TABLE demand_renamed RENAME COLUMN v TO renamed_v", 0))
			check()
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ViewRestore(t, p, dump)
				check()
			}
			// CASCADE is a positive dependency witness, not an invented error
			// record: both ignored-target views must depend on the original v.
			run(fixture.Exec("ALTER TABLE demand_renamed DROP COLUMN renamed_v CASCADE", 0))
			subqueryBoth(run, fixture.Select("SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_views WHERE viewname='grouping_v' OR viewname='grouping_copy') AS retained", []string{"retained"}, []string{"BOOL"}, [][]any{{false}}))
			subqueryBoth(run, fixture.Select("SELECT * FROM grouping_key_v", []string{"key_present"}, []string{"BOOL"}, [][]any{{true}}))
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
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				held := subqueryReviewHold(t, p, c, tc.query)
				check := func(rows [][]any) {
					s := fixture.Select(tc.query, []string{"scalar"}, []string{"INT4"}, rows)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check([][]any{{int64(1)}, {nil}})
				run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
				check([][]any{{nil}})
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				check([][]any{{int64(1)}, {nil}})
				run(fixture.Exec("CREATE VIEW distinct_v(result) AS "+tc.query, 0))
				fixture.ViewCopy(t, p, b, "distinct_v", "distinct_copy")
				storedCheck := func() {
					for _, view := range []string{"distinct_v", "distinct_copy"} {
						subqueryBoth(run, fixture.Select("SELECT * FROM "+view+" ORDER BY result", []string{"result"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
					}
				}
				storedCheck()
				if !b.PG {
					dump := fixture.ViewDump(t, p)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					fixture.ViewRestore(t, p, dump)
					storedCheck()
				}
			})
		})
	}
	t.Run("ignored-distinct-target", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE demand_outer(id INT)", 0))
			run(fixture.Exec("INSERT INTO demand_outer VALUES(1),(2)", 2))
			run(fixture.Exec("CREATE TABLE demand_bad(v INT)", 0))
			run(fixture.Exec("INSERT INTO demand_bad VALUES(1),(2)", 2))
			q := "SELECT EXISTS(SELECT DISTINCT(SELECT v FROM demand_bad) FROM demand_outer) AS present"
			held := subqueryReviewHold(t, p, c, q)
			s := fixture.Select(q, []string{"present"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("CREATE VIEW distinct_ignored_v AS "+q, 0))
			fixture.ViewCopy(t, p, b, "distinct_ignored_v", "distinct_ignored_copy")
			check := func() {
				for _, view := range []string{"distinct_ignored_v", "distinct_ignored_copy"} {
					subqueryBoth(run, fixture.Select("SELECT * FROM "+view, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			check()
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ViewRestore(t, p, dump)
				check()
			}
		})
	})
}
