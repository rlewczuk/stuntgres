package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryLockCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("NOWAIT/%v/%v", prepared, explicit), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					var x fixture.Executor = peer
					var reader *sql.Tx
					if explicit {
						reader = fixture.QueryLockBegin(t, p.Ctx, peer)
						x = reader
					}
					fixture.QueryLockNowait(t, p, b, x, "SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT", prepared, "t")
					if explicit {
						fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ImplicitError("SELECT id FROM t", fixture.TransactionAborted()))
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", false, "t")
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "id", 2)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", true, 4)
					// A failed INSERT SELECT publishes neither a result nor a partial batch.
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE dst(id INT)", 0)
					holder = fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					out := fixture.RowExecute(p.Ctx, peer, "INSERT INTO dst SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT", prepared)
					if out.Result != nil {
						t.Fatal("failed INSERT published result")
					}
					fixture.AssertDiagnostic(t, out.Err, ptrQueryLockDiagnostic(fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "t"`)))
					fixture.QueryLockRows(t, p, b, peer, "SELECT id FROM dst", false, "id")
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", false, 1)
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, cause := range []string{"caller", "lock timeout", "statement timeout"} {
			t.Run(fmt.Sprintf("cancel/%s/%v", cause, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					ap, bp := 0, 0
					if b.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, peer)
					}
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					reader := fixture.QueryLockBegin(t, p.Ctx, peer)
					q := "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE OF t"
					var out fixture.RelationOutcome
					if cause == "caller" && b.PG {
						done := fixture.RowAsync(t, p.Ctx, reader, q, prepared, true, b.Recorder.RawBackend)
						fixture.RowWait(t, p.Ctx, bp, ap)
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
						var canceled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						out = fixture.LockResult(t, done)
						terminal := fixture.QueryLockErrorOutcome(t, out, true, "id", []int64{1})
						referenceRowTimeoutDiagnostic(t, terminal, true, "57014", "canceling statement due to user request", "locking")
					} else if cause == "caller" {
						ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
						out = fixture.RowQuery(ctx, reader, q, prepared)
						cancel()
						terminal := fixture.QueryLockErrorOutcome(t, out, false, "id", nil)
						if !errors.Is(terminal, context.DeadlineExceeded) {
							t.Fatal(terminal)
						}
						var state interface{ SQLState() string }
						if errors.As(terminal, &state) {
							t.Fatal("fabricated caller SQLSTATE", terminal)
						}
					} else {
						setting := "lock_timeout"
						state := "55P03"
						if cause == "statement timeout" {
							setting = "statement_timeout"
							state = "57014"
						}
						fixture.LockExec(t, p.Ctx, reader, "SET LOCAL "+setting+"='30ms'", 0)
						out = fixture.RowQuery(p.Ctx, reader, q, prepared)
						terminal := fixture.QueryLockErrorOutcome(t, out, b.PG, "id", []int64{1})
						referenceRowTimeoutDiagnostic(t, terminal, b.PG, state, "canceling statement due to "+cause, "locking")
					}
					if err := reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Same physical owner immediately recovers; prefix is free, holder unaffected.
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					fixture.QueryLockRows(t, p, b, peer, "SELECT id FROM t ORDER BY id FOR UPDATE SKIP LOCKED", true, "id", 1, 3, 4)
					fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "t")
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id<3", false, 2)
				})
			})
		}
	}
	// Savepoint restores preexisting contributions but discards acquired-prefix
	// contributions from the failed statement. Root units cover waiter journals.
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
		reader := fixture.QueryLockBegin(t, p.Ctx, peer)
		fixture.QueryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=4 FOR SHARE", false, "id", 4)
		fixture.LockExec(t, p.Ctx, reader, "SAVEPOINT s", 0)
		fixture.QueryLockNowait(t, p, b, reader, "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE NOWAIT", true, "t")
		fixture.LockExec(t, p.Ctx, reader, "ROLLBACK TO SAVEPOINT s", 0)
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=1", false, 1)
		fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=4 FOR UPDATE NOWAIT", false, "t")
		fixture.QueryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=4 FOR SHARE NOWAIT", true, "id", 4)
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
	})
}
