package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCurrentOfNativeControls(t *testing.T) {
	t.Run("pool_persistence_and_driver_isolation", updateCurrentOfPoolPersistence)
	for _, replacement := range []string{"failed_restore", "reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT,CONSTRAINT positive CHECK(id>0))", 0))
				run(fixture.Exec("COMMENT ON COLUMN t.v IS 'positioned survivor'", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
				recordedCtx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				fetch, err := c.QueryContext(recordedCtx, "FETCH FORWARD 1 FROM c")
				if err != nil {
					t.Fatal(err)
				}
				defer fetch.Close()
				if b.PG {
					fixture.DeleteReferenceConsume(t, true, fetch, nil, currentOfFetch("", 1))
				}
				if !b.PG {
					// Eager native FETCH results are detached; close admission before host I/O,
					// retaining their owned rows until after replacement.
					frozen := fixture.SequenceFrozenRows{Executor: c, Rows: fetch, Query: "FETCH FORWARD 1 FROM c"}
					run(fixture.Exec("SAVEPOINT deletion", 0))
					result, err := c.ExecContext(p.Ctx, "UPDATE t SET v='positioned' WHERE CURRENT OF c")
					fixture.DeleteReferenceCount(t, result, err, 1)
					var private bytes.Buffer
					if err = stuntgres.Dump(p.Ctx, p.Native, &private); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(private.String(), "DECLARE") || strings.Contains(private.String(), "CURRENT OF") || !strings.Contains(private.String(), "positioned survivor") || !strings.Contains(private.String(), "NOT NULL") {
						t.Fatal("dump includes execution state or loses schema", private.String())
					}
					copyConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/private")
					if err != nil {
						t.Fatal(err)
					}
					copyDB := sql.OpenDB(copyConnector)
					defer copyDB.Close()
					if err = stuntgres.Restore(p.Ctx, copyDB, bytes.NewReader(private.Bytes())); err != nil {
						t.Fatal(err)
					}
					rows, err := copyDB.QueryContext(p.Ctx, "SELECT id,v FROM t ORDER BY id")
					fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
					run(fixture.Exec("ROLLBACK TO deletion", 0))
					retained, err := c.QueryContext(recordedCtx, "UPDATE t SET v='positioned' WHERE CURRENT OF c RETURNING id,v")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					returned := fixture.SequenceFrozenRows{Executor: c, Rows: retained, Query: "UPDATE t SET v='positioned' WHERE CURRENT OF c RETURNING id,v"}
					if replacement == "failed_restore" {
						var before bytes.Buffer
						if err = stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						canceled, cancel := context.WithCancel(p.Ctx)
						cancel()
						if err = stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE canceled(id INT)")); err == nil {
							t.Fatal("canceled Restore succeeded")
						}
						for _, script := range []string{"CREATE TABLE bad(id INT); INSERT INTO bad VALUES($1)", "UPDATE t SET v='positioned' WHERE CURRENT OF c;"} {
							if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
								t.Fatal("Restore accepted invalid mutation script")
							}
							var after bytes.Buffer
							if err = stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
								t.Fatal("failed Restore changed committed state", err)
							}
						}
						run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
						run(fixture.Exec("ROLLBACK TO deletion", 0))
						run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
						run(fixture.Exec("COMMIT", 0))
					} else {
						run(fixture.Exec("COMMIT", 0))
						var committed bytes.Buffer
						if err = stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
							t.Fatal(err)
						}
						// Restore committed survivor into another real public connector.
						finalConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/committed")
						if err != nil {
							t.Fatal(err)
						}
						finalDB := sql.OpenDB(finalConnector)
						defer finalDB.Close()
						if err = stuntgres.Restore(p.Ctx, finalDB, bytes.NewReader(committed.Bytes())); err != nil {
							t.Fatal(err)
						}
						rows, err = finalDB.QueryContext(p.Ctx, "SELECT id,v FROM t ORDER BY id")
						fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}, {int64(2), "two"}}))
						if _, err = finalDB.ExecContext(p.Ctx, "INSERT INTO t(v) VALUES('default')"); err != nil {
							t.Fatal(err)
						}
						rows, err = finalDB.QueryContext(p.Ctx, "SELECT id FROM t ORDER BY id")
						fixture.DeleteReferenceConsume(t, false, rows, err, currentOfFetch("", 1, 2, 9))
						if _, err = finalDB.ExecContext(p.Ctx, "INSERT INTO t VALUES(0,'bad')"); err == nil {
							t.Fatal("restored CHECK lost")
						}
						// Establish a live old incarnation before replacing with equal tuples.
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("DECLARE stale CURSOR FOR SELECT id FROM t WHERE id=2", 0))
						run(currentOfFetch("FETCH FORWARD 1 FROM stale", 2))
						if replacement == "reset" {
							err = stuntgres.Reset(p.Ctx, p.Native)
						} else {
							err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(committed.Bytes()))
						}
						if err != nil {
							t.Fatal(err)
						}
						r, e := c.ExecContext(p.Ctx, "UPDATE t SET v='positioned' WHERE CURRENT OF stale")
						if r != nil {
							t.Fatal("replacement fence exposed result")
						}
						fixture.TransactionOrdinary(t, e, "stuntgres: catalog replaced before publication")
						run(fixture.Exec("ROLLBACK", 0))
						if replacement == "reset" {
							if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(committed.Bytes())); err != nil {
								t.Fatal(err)
							}
						}
					}
					fixture.RunStep(t, p.Ctx, frozen, b.Recorder, currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					fixture.RunStep(t, p.Ctx, returned, b.Recorder, fixture.Select("UPDATE t SET v='positioned' WHERE CURRENT OF c RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "positioned"}}))
				} else {
					// PostgreSQL has SQL owner end, not host catalog controls.
					run(fixture.Exec("SAVEPOINT deletion", 0))
					run(currentOfFetch("UPDATE t SET v='positioned' WHERE CURRENT OF c RETURNING id", 1))
					run(fixture.Exec("ROLLBACK TO deletion", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2)
					run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
					run(fixture.Exec("COMMIT", 0))
				}
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2)
				run(fixture.Exec("BEGIN", 0))
				currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='positioned' WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				run(fixture.Exec("UPDATE t SET v='positioned' WHERE CURRENT OF c", 1))
				run(fixture.Exec("ROLLBACK", 0))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2)
			})
		})
	}
}
