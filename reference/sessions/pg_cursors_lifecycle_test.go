package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGCursorsLifecycle(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "driver_commit", "driver_rollback"} {
		t.Run(end, func(t *testing.T) {
			pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				var x fixture.Executor = c
				var tx *sql.Tx
				if end == "driver_commit" || end == "driver_rollback" {
					var err error
					tx, err = c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					x = tx
				} else {
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				}
				q := "DECLARE pgc_end NO SCROLL CURSOR FOR SELECT 1 AS i"
				sqlCursorStep(t, p, b, x, fixture.Exec(q, 0))
				pgcOne(t, p, b, x, "pgc_end", q, false, false)
				switch end {
				case "driver_commit":
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				case "driver_rollback":
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				default:
					sqlCursorStep(t, p, b, c, fixture.Exec(end, 0))
				}
				pgcEmpty(t, p, b, c, "pgc_end")
			})
		})
	}
	t.Run("hold_endings", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			q := "DECLARE pgc_kept SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows ORDER BY i"
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			row := pgcOne(t, p, b, c, "pgc_kept", q, true, true)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM pgc_kept", [][]any{{int64(1), "one"}}, b.PG))
			sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
			pgcSame(t, p, b, c, row)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			aborted := "DECLARE pgc_aborted NO SCROLL CURSOR WITH HOLD FOR SELECT 8 AS i"
			sqlCursorStep(t, p, b, c, fixture.Exec(aborted, 0))
			pgcOne(t, p, b, c, "pgc_aborted", aborted, true, false)
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			pgcEmpty(t, p, b, c, "pgc_aborted")
			pgcSame(t, p, b, c, row)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM pgc_kept", [][]any{{int64(2), nil}}, b.PG))
			for range 2 {
				sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE ALL", 0))
			}
			pgcEmpty(t, p, b, c, "pgc_kept", "pgc_aborted")
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM pgc_kept", sqlCursorMissing("pgc_kept")))
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("CLOSE pgc_kept", sqlCursorMissing("pgc_kept")))
		})
	})
	t.Run("savepoint_membership_not_motion", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			q := "DECLARE pgc_parent NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			parent := pgcOne(t, p, b, c, "pgc_parent", q, false, false)
			sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pgc_boundary", 0))
			child := "DECLARE pgc_child SCROLL CURSOR FOR SELECT 9 AS i"
			sqlCursorStep(t, p, b, c, fixture.Exec(child, 0))
			pgcOne(t, p, b, c, "pgc_child", child, false, true)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM pgc_parent", [][]any{{int64(1), "one"}}, b.PG))
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pgc_boundary", 0))
			pgcEmpty(t, p, b, c, "pgc_child")
			pgcSame(t, p, b, c, parent)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM pgc_parent", [][]any{{int64(2), nil}}, b.PG))
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_parent", 0))
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pgc_boundary", 0))
			pgcEmpty(t, p, b, c, "pgc_parent", "pgc_child")
		})
	})
	t.Run("failed_producer_stays_visible", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			q := "DECLARE pgc_failed NO SCROLL CURSOR FOR SELECT 10/(i-2) AS v FROM cursor_rows ORDER BY i"
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			row := pgcOne(t, p, b, c, "pgc_failed", q, false, false)
			sqlCursorError(t, p, b, c, "FETCH ALL FROM pgc_failed", sqlCursorDiagnostic("22012", "division by zero"))
			pgcSame(t, p, b, c, row)
			sqlCursorError(t, p, b, c, "FETCH ALL FROM pgc_failed", sqlCursorDiagnostic("55000", "portal \"pgc_failed\" cannot be run"))
			pgcSame(t, p, b, c, row)
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_failed", 0))
			pgcEmpty(t, p, b, c, "pgc_failed")
		})
	})
}
