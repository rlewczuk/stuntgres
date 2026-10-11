package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCurrentOfRollbackAndRepeat(t *testing.T) {
	currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
		run(fixture.Exec("SAVEPOINT before_motion", 0))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
		run(fixture.Exec("ROLLBACK TO before_motion", 0))
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
		run(fixture.Exec("SAVEPOINT lifecycle", 0))
		run(fixture.Exec("DECLARE later CURSOR FOR SELECT id FROM t", 0))
		run(fixture.Exec("CLOSE c", 0))
		run(fixture.Exec("ROLLBACK TO lifecycle", 0))
		for _, name := range []string{"later", "c"} {
			currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF "+name, "34000", `cursor "`+name+`" does not exist`, true))
		}
		run(fixture.Exec("ROLLBACK", 0))
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		run(fixture.Exec("BEGIN", 0))
		currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceDeleteCurrentOfLifetime(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		t.Run(end, func(t *testing.T) {
			currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec(end, 0))
				if end == "COMMIT" || end == "ROLLBACK" {
					run(fixture.Exec("BEGIN", 0))
				}
				currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, true))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2", 0))
				currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "24000", `cursor "c" is not positioned on a row`, false))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3)
			})
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "driver_rollback", true: "driver_commit"}[commit], func(t *testing.T) {
			currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				tr := updateFromRun(t, p, b, tx)
				tr(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				tr(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				tr(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				ids := []int64{1, 2, 3}
				if commit {
					ids = []int64{2, 3}
				}
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, ids...)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
			})
		})
	}
	t.Run("handle_ownership_and_preconditions", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				// This scenario owns its borrowers because it explicitly returns/replaces
				// them. Do not pass a manually closed borrower to updateFromReference's
				// fixture-owned cleanup.
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				release := func() {
					if c != nil {
						if err := c.Close(); err != nil {
							t.Error(err)
						}
						c = nil
					}
				}
				defer func() {
					if c != nil {
						c.ExecContext(p.Ctx, "ROLLBACK")
					}
					release()
				}()
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				s, err := c.PrepareContext(p.Ctx, "FETCH FORWARD 1 FROM c")
				if err != nil {
					t.Fatal(err)
				}
				rows, err := s.QueryContext(p.Ctx)
				fixture.DeleteReferenceConsume(t, b.PG, rows, err, currentOfFetch("", 1))
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				// Returning the borrower is not a physical session end. Restrict pooling so
				// reacquisition is the same idle physical connection, without relying on PID.
				b.DB.SetMaxOpenConns(1)
				release()
				c, err = b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				run = updateFromRun(t, p, b, c)
				run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN READ ONLY", 0))
				currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF absent", "25006", "cannot execute DELETE in a read-only transaction", true))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN", 0))
				fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError("DELETE FROM t WHERE CURRENT OF absent", "34000", `cursor "absent" does not exist`, false))
				fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError("DELETE FROM t WHERE CURRENT OF absent", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
				// Closing the last pool destroys the physical session, unlike borrower close.
				release()
				b.DB.SetMaxIdleConns(0)
				c, err = b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fresh := c
				fr := updateFromRun(t, p, b, fresh)
				fr(fixture.Exec("BEGIN", 0))
				currentOfFailure(t, p, b, fresh, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
				fixture.DeleteReferenceRows(t, p, fresh, b.Recorder, 1, 2, 3)
			})
		}
	})
	t.Run("prepared_transaction", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			run(fixture.Exec("PREPARE TRANSACTION 'current_of_lifetime'", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED 'current_of_lifetime'")
			run(fixture.Exec("BEGIN", 0))
			currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("ROLLBACK PREPARED 'current_of_lifetime'", 0))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		})
	})
	t.Run("authority", currentOfAuthority)
}

func TestReferenceDeleteCurrentOfFeatureSmoke(t *testing.T) {
	currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		q := "DELETE FROM t WHERE CURRENT OF c RETURNING id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		run(fixture.Exec("SAVEPOINT before_delete", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, currentOfFetch(q, 1))
		run(fixture.Exec("ROLLBACK TO before_delete", 0))
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, currentOfFetch(q, 1))
		run(currentOfFetch("FETCH 2 FROM c", 2, 3))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, currentOfFetch(q, 3))
		run(fixture.Exec("COMMIT", 0))
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2)
		run(fixture.Exec("BEGIN", 0))
		currentOfFailure(t, p, b, c, currentOfError(q, "34000", `cursor "c" does not exist`, false))
		run(fixture.Exec("ROLLBACK", 0))
	})
	t.Run("physical_duplicate", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7),(7)", 2))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 7))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 7)
		})
	})
}
