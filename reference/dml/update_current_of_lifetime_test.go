package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func updateCurrentOfRollbackAndRepeat(t *testing.T) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
		run(fixture.Exec("SAVEPOINT before_motion", 0))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}, {int64(2), "two"}, {int64(3), "three"}}))
		run(fixture.Exec("ROLLBACK TO before_motion", 0))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}, {int64(2), "two"}, {int64(3), "three"}}))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
		run(fixture.Exec("SAVEPOINT lifecycle", 0))
		run(fixture.Exec("DECLARE later CURSOR FOR SELECT id FROM t", 0))
		run(fixture.Exec("CLOSE c", 0))
		run(fixture.Exec("ROLLBACK TO lifecycle", 0))
		for _, name := range []string{"later", "c"} {
			currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF "+name, "34000", `cursor "`+name+`" does not exist`, true))
		}
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		run(fixture.Exec("BEGIN", 0))
		currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceUpdateCurrentOfLifetimeAndRollback(t *testing.T) {
	t.Run("physical_session_isolation", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			other := updateFromRun(t, p, b, peer)
			other(fixture.Exec("BEGIN", 0))
			defer peer.ExecContext(p.Ctx, "ROLLBACK")
			currentOfFailure(t, p, b, peer, currentOfError("UPDATE t SET v='wrong' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, true))
			other(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2", 0))
			other(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
			other(fixture.Exec("UPDATE t SET v='peer' WHERE CURRENT OF c", 1))
			run(fixture.Exec("UPDATE t SET v='owner' WHERE CURRENT OF c", 1))
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "owner"}, {int64(2), "two"}, {int64(3), "three"}}))
			other(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "peer"}, {int64(3), "three"}}))
		})
	})
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		t.Run(end, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
				currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, true))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2", 0))
				currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "24000", `cursor "c" is not positioned on a row`, false))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "positioned"}, {int64(3), "three"}}))
			})
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "driver_rollback", true: "driver_commit"}[commit], func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
				tr(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
				tr(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}, {int64(2), "two"}, {int64(3), "three"}}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				value := "one"
				if commit {
					value = "positioned"
				}
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), value}, {int64(2), "two"}, {int64(3), "three"}}))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
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
				run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}, {int64(2), "two"}, {int64(3), "three"}}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
				run(fixture.Exec("BEGIN READ ONLY", 0))
				currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF absent", "25006", "cannot execute UPDATE in a read-only transaction", true))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN", 0))
				fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF absent", "34000", `cursor "absent" does not exist`, false))
				fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF absent", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
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
				currentOfFailure(t, p, b, fresh, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
				fr(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
			})
		}
	})
	t.Run("prepared_transaction", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
			run(fixture.Exec("PREPARE TRANSACTION 'current_of_lifetime'", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED 'current_of_lifetime'")
			run(fixture.Exec("BEGIN", 0))
			currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("ROLLBACK PREPARED 'current_of_lifetime'", 0))
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		})
	})
	t.Run("rollback_without_rewind", updateCurrentOfRollbackAndRepeat)
}
