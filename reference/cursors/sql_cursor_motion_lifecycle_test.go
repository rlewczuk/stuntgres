package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorScrollLifecycleAndAuthority(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorMotionStart(t, p, b, c, step)
		step(sqlCursorBatch("FETCH ALL FROM c", sqlCursorMotionRows(1, 2, 3), b.PG))
		step(fixture.Exec("SAVEPOINT parent", 0))
		step(fixture.Exec("MOVE PRIOR FROM c", 1))
		step(fixture.Exec("MOVE PRIOR FROM c", 1))
		step(fixture.Exec("DECLARE child SCROLL CURSOR FOR SELECT i FROM cursor_rows", 0))
		step(fixture.Exec("ROLLBACK TO parent", 0))
		step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(2), b.PG))
		sqlCursorError(t, p, b, c, "MOVE NEXT FROM child", sqlCursorMissing("child"))
		// Closing a held utility statement/public batch never closes its name.
		stmt, err := c.PrepareContext(p.Ctx, "MOVE PRIOR FROM c")
		if err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "MOVE PRIOR FROM c"}, fixture.Exec("MOVE PRIOR FROM c", 1))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		rows, err := c.QueryContext(p.Ctx, "FETCH NEXT FROM c")
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		step(sqlCursorBatch("FETCH PRIOR FROM c", sqlCursorMotionRows(1), b.PG))
		step(fixture.Exec("SAVEPOINT closing", 0))
		step(fixture.Exec("CLOSE c", 0))
		step(fixture.Exec("ROLLBACK TO closing", 0))
		sqlCursorError(t, p, b, c, "MOVE NEXT FROM c", sqlCursorMissing("c"))
		step(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
		step(fixture.Exec("MOVE ALL FROM c", 3))
		step(sqlCursorBatch("FETCH PRIOR FROM c", sqlCursorMotionRows(3), b.PG))
		step(fixture.Exec("COMMIT", 0))
		step(fixture.ErrorDiagnostic("MOVE PRIOR FROM c", sqlCursorMissing("c")))
	})
	for _, end := range []string{"driver-commit", "driver-rollback", "physical-discard"} {
		t.Run(end, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				sqlCursorStep(t, p, b, tx, fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i FROM cursor_rows", 0))
				sqlCursorStep(t, p, b, tx, fixture.Exec("MOVE ALL FROM c", 3))
				sqlCursorStep(t, p, b, tx, fixture.Exec("MOVE PRIOR FROM c", 1))
				if end == "driver-commit" {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if end == "physical-discard" {
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i FROM cursor_rows", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("MOVE ALL FROM c", 3))
					if err := c.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
						t.Fatal(err)
					}
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					sqlCursorStep(t, p, b, other, fixture.ErrorDiagnostic("MOVE PRIOR FROM c", sqlCursorMissing("c")))
				} else {
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("MOVE PRIOR FROM c", sqlCursorMissing("c")))
				}
			})
		})
	}
	// Cancellation uses the existing public execution policy; no saved DECLARE
	// context may authorize or disable a later MOVE operation.
	t.Run("current_cancel", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// Only native promises rejection before admission without losing the
			// physical owner. pq cancellation can asynchronously retire its socket.
			if b.PG {
				return
			}
			sqlCursorSetup(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "MOVE NEXT FROM c")
			if err != context.Canceled || result != nil {
				t.Fatalf("pre-admission cancel result=%v err=%v", result, err)
			}
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(1), b.PG))
		})
	})
}

func TestReferenceSQLCursorScrollCurrentOf(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		step(fixture.Exec("CREATE TABLE motion_identity(id INTEGER,t TEXT)", 0))
		step(fixture.Exec("INSERT INTO motion_identity VALUES(1,'same'),(2,'same'),(3,NULL)", 3))
		step(fixture.Exec("BEGIN", 0))
		// No ORDER BY/materialization or locking mark: retain the eligible plain
		// physical scan. Values cannot identify either of the duplicate rows.
		step(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT t FROM motion_identity", 0))
		step(fixture.Exec("SAVEPOINT mutation", 0))
		step(fixture.Exec("MOVE ABSOLUTE 2 FROM c", 1))
		step(fixture.Exec("UPDATE motion_identity SET t='second' WHERE CURRENT OF c", 1))
		step(fixture.Select("FETCH PRIOR FROM c", []string{"t"}, []string{"TEXT"}, [][]any{{"same"}}))
		step(fixture.Exec("DELETE FROM motion_identity WHERE CURRENT OF c", 1))
		step(fixture.Select("SELECT id,t FROM motion_identity ORDER BY id", []string{"id", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "second"}, {int64(3), nil}}))
		step(fixture.Exec("ROLLBACK TO mutation", 0))
		step(fixture.Exec("UPDATE motion_identity SET t='first' WHERE CURRENT OF c", 1))
		step(fixture.Select("SELECT id,t FROM motion_identity ORDER BY id", []string{"id", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "first"}, {int64(2), "same"}, {int64(3), nil}}))
		step(fixture.Exec("MOVE ABSOLUTE 0 FROM c", 0))
		want := sqlCursorDiagnostic("24000", "cursor \"c\" is not positioned on a row")
		sqlCursorError(t, p, b, c, "UPDATE motion_identity SET t='bad' WHERE CURRENT OF c", want)
		step(fixture.Exec("MOVE ALL FROM c", 3))
		sqlCursorError(t, p, b, c, "DELETE FROM motion_identity WHERE CURRENT OF c", want)
	})
}
