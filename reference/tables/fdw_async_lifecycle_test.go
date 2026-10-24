package reference_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAsyncCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%v_insert_%v", prepared, insert), func(t *testing.T) {
				fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
					c.run(t, fixture.Exec("CREATE TABLE async_target(id integer,label text)", 0))
					c.run(t, fixture.Exec("INSERT INTO async_target VALUES(99,'keep')", 1))
					query := fdwAsyncQuery
					if insert {
						query = "INSERT INTO async_target " + query
					}
					execute := func(ctx context.Context) error {
						if insert {
							_, err := c.conn.ExecContext(ctx, query)
							return err
						}
						return fdwAsyncCollect(c.conn.QueryContext(ctx, query)).err
					}
					if prepared {
						stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						execute = func(ctx context.Context) error {
							if insert {
								_, err := stmt.ExecContext(ctx)
								return err
							}
							return fdwAsyncCollect(stmt.QueryContext(ctx)).err
						}
					}
					var pid int
					if c.b.PG {
						if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
					}
					ctx, cancel := context.WithCancel(c.p.Ctx)
					done := make(chan error, 1)
					go func() { done <- execute(ctx) }()
					joined := false
					defer func() {
						c.release()
						cancel()
						if !joined {
							<-done
						}
					}()
					observe, stop := context.WithTimeout(c.p.Ctx, 5*time.Second)
					witness := c.witness(observe, true)
					stop()
					if witness != nil {
						t.Fatal(witness)
					}
					if c.b.PG {
						var signaled bool
						if err := fixture.Admin.QueryRowContext(c.p.Ctx, "SELECT COALESCE((SELECT pg_cancel_backend(pid) FROM pg_stat_activity WHERE pid=$1 AND state='active' AND query=$2),false)", pid, query).Scan(&signaled); err != nil || !signaled {
							t.Fatalf("active owner cancellation: signaled=%v error=%v", signaled, err)
						}
					} else {
						cancel()
					}
					// Cancellation must stop and join the real request while the external gate
					// remains held. Releasing it first would allow ordinary success to win.
					joinCtx, stopJoin := context.WithTimeout(c.p.Ctx, 5*time.Second)
					defer stopJoin()
					var err error
					select {
					case err = <-done:
						joined = true
					case <-joinCtx.Done():
						t.Fatalf("canceled query did not join with source gate held: %v", joinCtx.Err())
					}
					if c.b.PG {
						fixture.AssertDiagnostic(t, err, new(fdwDiagnostic("57014", "canceling statement due to user request")))
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel returned %v", err)
					}
					c.idle(t, true)
					c.release()
					// Use another borrower: pq may close the interrupted physical transport.
					fixture.RunStep(t, c.p.Ctx, c.b.DB, c.b.Recorder, fdwAsyncRows("SELECT id,label FROM async_target", [][]any{{int64(99), "keep"}}))
					fixture.RunStep(t, c.p.Ctx, c.b.DB, c.b.Recorder, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
				})
			})
		}
	}
}

func TestReferenceFDWAsyncClose(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("finite_prepared_%v", prepared), func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				c.run(t, fixture.Exec("BEGIN", 0))
				c.run(t, fixture.Exec("DECLARE async_cursor NO SCROLL CURSOR FOR "+fdwAsyncQuery, 0))
				// DECLARE is lazy: no source request is needed before the first FETCH.
				if !c.b.PG {
					_, _, requests := c.reader.counts()
					if requests != 0 {
						t.Fatal("DECLARE started remote requests")
					}
				}
				var all [][]any
				fetch := func(query string, gated bool) {
					var result fdwAsyncResult
					if prepared {
						stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						if gated {
							result = fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult { return fdwAsyncCollect(stmt.QueryContext(ctx)) })
						} else {
							result = fdwAsyncCollect(stmt.QueryContext(c.p.Ctx))
						}
					} else if gated {
						result = fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult { return fdwAsyncCollect(c.conn.QueryContext(ctx, query)) })
					} else {
						result = fdwAsyncCollect(c.conn.QueryContext(c.p.Ctx, query))
					}
					if result.err != nil {
						t.Fatal(result.err)
					}
					if len(result.columns) != 2 || result.columns[0] != "id" || len(result.types) != 2 || result.types[0] != "INT4" || result.types[1] != "TEXT" {
						t.Fatalf("FETCH metadata %v %v", result.columns, result.types)
					}
					all = append(all, result.rows...)
					c.idle(t, false)
				}
				fetch("FETCH 1 FROM async_cursor", true)
				if len(all) != 1 {
					t.Fatalf("first batch %v", all)
				}
				first := fmt.Sprintf("%#v", all)
				fetch("FETCH 2 FROM async_cursor", false)
				if len(all) != 3 {
					t.Fatalf("second batch %v", all)
				}
				if fmt.Sprintf("%#v", all[:1]) != first {
					t.Fatal("prior batch mutated")
				}
				fetch("FETCH ALL FROM async_cursor", false)
				fdwAsyncAssert(t, fdwAsyncResult{rows: all, columns: []string{"id", "label"}, types: []string{"INT4", "TEXT"}}, fdwAsyncBag)
				c.run(t, fixture.Exec("CLOSE async_cursor", 0))
				c.run(t, fixture.Exec("COMMIT", 0))
				c.idle(t, true)
			})
		})
	}
	for _, terminal := range []string{"CLOSE async_cursor", "ROLLBACK", "session"} {
		t.Run(terminal, func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				c.run(t, fixture.Exec("BEGIN", 0))
				c.run(t, fixture.Exec("DECLARE async_cursor NO SCROLL CURSOR FOR "+fdwAsyncQuery, 0))
				result := fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult {
					return fdwAsyncCollect(c.conn.QueryContext(ctx, "FETCH 1 FROM async_cursor"))
				})
				if result.err != nil || len(result.rows) != 1 {
					t.Fatalf("FETCH %v %v", result.rows, result.err)
				}
				if terminal == "session" {
					if err := c.conn.Close(); err != nil {
						t.Fatal(err)
					}
					c.b.DB.SetMaxIdleConns(0)
				} else {
					c.run(t, fixture.Exec(terminal, 0))
				}
				c.idle(t, true)
			})
		})
	}
	t.Run("limit_pending", func(t *testing.T) {
		fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
			if !c.b.PG {
				c.reader.mu.Lock()
				c.reader.readyAfterSlow = true
				c.reader.mu.Unlock()
			}
			result := fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult {
				return fdwAsyncCollect(c.conn.QueryContext(ctx, "SELECT id,label FROM ("+fdwAsyncQuery+") AS combined LIMIT 1"))
			})
			if result.err != nil || len(result.rows) != 1 {
				t.Fatalf("LIMIT %v %v", result.rows, result.err)
			}
			found := false
			for _, row := range fdwAsyncBag {
				if fmt.Sprint(row) == fmt.Sprint(result.rows[0]) {
					found = true
				}
			}
			if !found {
				t.Fatalf("unexpected LIMIT row %v", result.rows)
			}
			c.idle(t, true)
		})
	})
}

func TestReferenceFDWAsyncErrors(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			t.Run(fmt.Sprintf("bad_integer_prepared_%v_insert_%v", prepared, insert), func(t *testing.T) {
				fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
					c.release()
					c.source(t, "CREATE TABLE async_source.bad(id text,label text)")
					c.source(t, "INSERT INTO async_source.bad VALUES('bad-integer','bad')")
					c.run(t, fixture.Exec("CREATE FOREIGN TABLE async_bad(id integer,label text) SERVER async_slow_server OPTIONS(schema_name 'async_source',table_name 'bad')", 0))
					c.run(t, fixture.Exec("CREATE TABLE async_target(id integer,label text)", 0))
					c.run(t, fixture.Exec("INSERT INTO async_target VALUES(99,'keep')", 1))
					query := "SELECT id,label FROM async_bad UNION ALL SELECT id,label FROM async_ready"
					if insert {
						query = "INSERT INTO async_target " + query
					}
					var err error
					if prepared {
						stmt, e := c.conn.PrepareContext(c.p.Ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						if insert {
							_, err = stmt.ExecContext(c.p.Ctx)
						} else {
							err = fdwAsyncCollect(stmt.QueryContext(c.p.Ctx)).err
						}
					} else if insert {
						_, err = c.conn.ExecContext(c.p.Ctx, query)
					} else {
						err = fdwAsyncCollect(c.conn.QueryContext(c.p.Ctx, query)).err
					}
					want := fdwDiagnostic("22P02", `invalid input syntax for type integer: "bad-integer"`)
					// Exact remote conversion Context is oracle-discovered, not inferred from native output.
					want.Context = `column "id" of foreign table "async_bad"`
					fixture.AssertDiagnostic(t, err, &want)
					c.run(t, fdwAsyncRows("SELECT id,label FROM async_target", [][]any{{int64(99), "keep"}}))
					c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
					c.idle(t, true)
				})
			})
		}
	}
	t.Run("transport_abort", func(t *testing.T) {
		fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
			c.release()
			c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
			if !c.b.PG {
				c.reader.mu.Lock()
				c.reader.abort = true
				c.reader.mu.Unlock()
				result := fdwAsyncCollect(c.conn.QueryContext(c.p.Ctx, fdwAsyncQuery))
				if result.err == nil {
					t.Fatal("aborted real HTTP connection succeeded")
				}
				var state interface{ SQLState() string }
				if errors.As(result.err, &state) {
					t.Fatalf("transport fabricated SQLSTATE: %v", result.err)
				}
				c.idle(t, true)
			}
		})
	})
}
