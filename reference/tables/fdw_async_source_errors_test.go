package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAsyncSourceErrors(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%v_insert_%v", prepared, insert), func(t *testing.T) {
				fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
					c.source(t, "CREATE TABLE async_source.gone(id integer)")
					if c.b.PG {
						c.source(t, "CREATE OR REPLACE FUNCTION async_source.gated(integer) RETURNS integer LANGUAGE plpgsql VOLATILE AS $$ BEGIN PERFORM pg_advisory_xact_lock(7182401); PERFORM id FROM async_source.gone; RETURN $1; END $$")
					} else {
						c.reader.mu.Lock()
						c.reader.failSQL = "SELECT id FROM async_source.gone"
						c.reader.mu.Unlock()
					}
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
					ctx, cancel := context.WithCancel(c.p.Ctx)
					defer cancel()
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
					// The query is planned, both remote scans have begun, and the delayed FETCH
					// is live before the referenced source is really removed.
					c.source(t, "DROP TABLE async_source.gone")
					c.release()
					err := <-done
					joined = true
					want := fdwDiagnostic("42P01", `relation "async_source.gone" does not exist`)
					if c.b.PG {
						want.Context = "PL/pgSQL function async_source.gated(integer) line 1 at PERFORM\nremote SQL command: SELECT id, label FROM async_source.slow_gate"
					} else {
						want.Position = fixture.Position(16)
					}
					fixture.AssertDiagnostic(t, err, &want)
					c.run(t, fdwAsyncRows("SELECT id,label FROM async_target", [][]any{{int64(99), "keep"}}))
					c.idle(t, true)
					c.source(t, "CREATE TABLE async_source.gone(id integer)")
					c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
				})
			})
		}
	}
}
