package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ksLifeReplacementWaitCases(t *testing.T) {
	t.Helper()
	for _, replacement := range []string{"drop_recreate", "reset"} {
		t.Run("native_wait_"+replacement, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if b.PG {
					return
				} // Native epoch/storage fencing, not a server reset simulator.
				a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
				holder := freezeConn(t, p, b)
				defer holder.Close()
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE", 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					rows, err := c.QueryContext(ctx, killSQL, "ks_part.heap", freezeTIDs(a.tid))
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				joined := false
				defer func() {
					if !joined {
						cancel()
						out := await()
						if out.Rows != nil {
							out.Rows.Close()
						}
					}
				}()
				fixture.RelationLockPending(t, p, b, 0, 0, 0, "RowExclusiveLock", finished)
				if replacement == "reset" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal("reset while stale operation waits", err)
					}
					// Reset fences old transaction resources; do not COMMIT its
					// stale captured catalog or reuse it for new replacement DDL.
					replacer := freezeConn(t, p, b)
					defer replacer.Close()
					for _, q := range []string{"CREATE SCHEMA ks_part", "CREATE EXTENSION pg_surgery SCHEMA ks_part", "CREATE TABLE ks_part.heap(k integer,v text)", "INSERT INTO ks_part.heap VALUES(1,'replacement')"} {
						count := int64(0)
						if q == "INSERT INTO ks_part.heap VALUES(1,'replacement')" {
							count = 1
						}
						freezeRun(t, p, b, replacer, fixture.Exec(q, count))
					}
				} else {
					freezeRun(t, p, b, holder, fixture.Exec("DROP TABLE ks_part.heap", 0))
					freezeRun(t, p, b, holder, fixture.Exec("CREATE TABLE ks_part.heap(k integer,v text)", 0))
					freezeRun(t, p, b, holder, fixture.Exec("INSERT INTO ks_part.heap VALUES(1,'replacement')", 1))
					freezeRun(t, p, b, holder, fixture.Exec("COMMIT", 0))
				}
				out := await()
				joined = true
				if out.Rows != nil {
					out.Rows.Close()
				}
				var state interface{ SQLState() string }
				if out.Err == nil || out.Err.Error() != "stuntgres: catalog replaced before publication" || errors.As(out.Err, &state) {
					t.Fatal("replacement must fence ordinary captured authority", out.Err)
				}
				reader := freezeConn(t, p, b)
				defer reader.Close()
				ksLifeRows(t, p, b, reader, []any{int64(1), "replacement"})
				// No leaked surgery contribution survives the stale request.
				freezeRun(t, p, b, reader, fixture.Exec("BEGIN", 0))
				defer reader.ExecContext(p.Ctx, "ROLLBACK")
				freezeRun(t, p, b, reader, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				freezeRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
