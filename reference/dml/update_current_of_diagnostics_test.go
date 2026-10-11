package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// All CURRENT OF executor records below are independently derived from
// REL_18_0 execCurrent.c. Empty ancillary fields are deliberate expectations.
func TestReferenceUpdateCurrentOfEligibilityAndDiagnostics(t *testing.T) {
	t.Run("syntax_and_analysis", updateCurrentOfSyntaxAndAnalysis)
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			for _, tc := range []struct{ name, query, fetch, code, message string }{
				{"simple_before", "SELECT id FROM t", "", "24000", `cursor "c" is not positioned on a row`},
				{"wrong_before", "SELECT id FROM other", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"locked_wrong_before", "SELECT id FROM other FOR UPDATE", "", "24000", `cursor "c" does not have a FOR UPDATE/SHARE reference to table "t"`},
				{"multiple_marks", "SELECT a.id FROM t a JOIN t z ON a.id=z.id FOR UPDATE OF a,z", "", "24000", `cursor "c" has multiple FOR UPDATE/SHARE references to table "t"`},
				{"aggregate", "SELECT count(*) FROM t", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"group", "SELECT id FROM t GROUP BY id", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"distinct", "SELECT DISTINCT id FROM t", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"set", "SELECT id FROM t UNION SELECT id FROM other", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"join", "SELECT t.id FROM t JOIN other ON t.id=other.id", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"constant", "SELECT 1", "", "24000", `cursor "c" is not a simply updatable scan of table "t"`},
				{"empty", "SELECT id FROM t", "FETCH FORWARD 1 FROM c", "24000", `cursor "c" is not positioned on a row`},
				{"all_eof", "SELECT id FROM t FOR UPDATE", "FETCH ALL FROM c", "24000", `cursor "c" is not positioned on a row`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := updateFromRun(t, p, b, c)
						run(fixture.Exec("CREATE TABLE t(id INT)", 0))
						run(fixture.Exec("CREATE TABLE other(id INT)", 0))
						if tc.name != "empty" {
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						}
						run(fixture.Exec("INSERT INTO other VALUES(1)", 1))
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("DECLARE c CURSOR FOR "+tc.query, 0))
						if tc.fetch != "" {
							if tc.name == "all_eof" {
								run(currentOfFetch(tc.fetch, 1))
							} else {
								run(currentOfFetch(tc.fetch))
							}
						}
						currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET id=id WHERE CURRENT OF c", tc.code, tc.message, prepared))
						if tc.name == "empty" {
							fixture.DeleteReferenceRows(t, p, c, b.Recorder)
						} else {
							fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1)
						}
					})
				})
			}
			t.Run("missing_closed", func(t *testing.T) {
				updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					s := currentOfError("UPDATE t SET id=id WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, prepared)
					currentOfFailure(t, p, b, c, s)
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					run(fixture.Exec("CLOSE c", 0))
					currentOfFailure(t, p, b, c, s)
				})
			})
		})
	}
	for _, lock := range []string{"", " FOR UPDATE", " FOR NO KEY UPDATE", " FOR SHARE", " FOR KEY SHARE"} {
		t.Run("eligible"+strings.ReplaceAll(lock, " ", "_"), func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2"+lock, 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				run(fixture.Exec("UPDATE t SET id=id WHERE CURRENT OF c", 1))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
			})
		})
	}
	for _, query := range []string{"SELECT t.id FROM t JOIN other ON t.id=other.id FOR UPDATE OF t", "SELECT id FROM (SELECT id FROM t) x", "SELECT id FROM visible"} {
		t.Run(query, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("CREATE TABLE other(id INT)", 0))
				run(fixture.Exec("CREATE VIEW visible AS SELECT id FROM t", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO other VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR "+query, 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("UPDATE t SET id=id WHERE CURRENT OF c", 1))
			})
		})
	}
	t.Run("physical_plan", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("CREATE INDEX current_order ON t(id)", 0))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			if b.PG {
				run(fixture.Exec("SET LOCAL enable_indexscan=off", 0))
				run(fixture.Exec("SET LOCAL enable_indexonlyscan=off", 0))
				run(fixture.Exec("SET LOCAL enable_bitmapscan=off", 0))
				currentOfPlan(t, p, c, "SELECT id FROM t ORDER BY id", "Sort")
			}
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET id=id WHERE CURRENT OF c", "24000", `cursor "c" is not a simply updatable scan of table "t"`, false))
			run(fixture.Exec("CLOSE c", 0))
			if b.PG {
				run(fixture.Exec("SET LOCAL enable_indexscan=on", 0))
				run(fixture.Exec("SET LOCAL enable_indexonlyscan=on", 0))
				run(fixture.Exec("SET LOCAL enable_seqscan=off", 0))
				run(fixture.Exec("SET LOCAL enable_sort=off", 0))
				currentOfPlan(t, p, c, "SELECT id FROM t ORDER BY id", "Index")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("UPDATE t SET id=id WHERE CURRENT OF c", 1))
			} else {
				// Native has a physical sort frontier, not a PostgreSQL index-order path.
				// Its supported scan alternative independently proves eligibility.
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("UPDATE t SET id=id WHERE CURRENT OF c", 1))
			}
		})
	})
	t.Run("failed_fetch_executor", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id,10/(id-id) AS bad FROM t WHERE id=1 FOR UPDATE", 0))
			run(fixture.Exec("SAVEPOINT demand", 0))
			s := currentOfError("FETCH FORWARD 1 FROM c", "22012", "division by zero", false)
			s.SelectRows = true
			run(s)
			run(fixture.Exec("ROLLBACK TO demand", 0))
			// PostgreSQL 18.0 removes the failed executor's queryDesc. CURRENT OF
			// reports its executor-lifetime error, not FETCH's portal55000.
			currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET id=id WHERE CURRENT OF c", "24000", `cursor "c" is held from a previous transaction`, false))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		})
	})
	t.Run("quoted_utf8_diagnostics", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec(`DECLARE "é""C" CURSOR FOR SELECT id FROM t WHERE id=1`, 0))
			q := `UPDATE t SET id=id WHERE CURRENT OF "é""C"`
			for _, prepared := range []bool{false, true} {
				currentOfFailure(t, p, b, c, currentOfError(q, "24000", `cursor "é"C" is not positioned on a row`, prepared))
			}
			run(currentOfFetch(`FETCH FORWARD 1 FROM "é""C"`, 1))
			run(fixture.Exec(q, 1))
			run(fixture.Exec(`CLOSE "é""C"`, 0))
			for _, prepared := range []bool{false, true} {
				currentOfFailure(t, p, b, c, currentOfError(q, "34000", `cursor "é"C" does not exist`, prepared))
			}
		})
	})
}
