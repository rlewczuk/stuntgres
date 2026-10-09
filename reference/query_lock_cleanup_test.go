package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceQueryLockCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("NOWAIT/%v/%v", prepared, explicit), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					var x referenceExecutor = peer
					var reader *sql.Tx
					if explicit {
						reader = queryLockBegin(t, p.ctx, peer)
						x = reader
					}
					queryLockNowait(t, p, b, x, "SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT", prepared, "t")
					if explicit {
						referenceRunStep(t, p.ctx, reader, b.recorder, referenceImplicitError("SELECT id FROM t", referenceTransactionAborted()))
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					queryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", false, "t")
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "id", 2)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", true, 4)
					// A failed INSERT SELECT publishes neither a result nor a partial batch.
					referenceLockExec(t, p.ctx, a, "CREATE TABLE dst(id INT)", 0)
					holder = queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					out := referenceRowExecute(p.ctx, peer, "INSERT INTO dst SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT", prepared)
					if out.result != nil {
						t.Fatal("failed INSERT published result")
					}
					referenceAssertDiagnostic(t, out.err, ptrQueryLockDiagnostic(controlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "t"`)))
					queryLockRows(t, p, b, peer, "SELECT id FROM dst", false, "id")
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", false, 1)
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, cause := range []string{"caller", "lock timeout", "statement timeout"} {
			t.Run(fmt.Sprintf("cancel/%s/%v", cause, prepared), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					ap, bp := 0, 0
					if b.pg {
						ap, bp = referenceRowPIDs(t, p.ctx, a, peer)
					}
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					reader := queryLockBegin(t, p.ctx, peer)
					q := "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE OF t"
					var out relationOutcome
					if cause == "caller" && b.pg {
						done := referenceRowAsync(t, p.ctx, reader, q, prepared, true, b.recorder.rawBackend)
						referenceRowWait(t, p.ctx, bp, ap)
						c, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						queryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
						var canceled bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						out = referenceLockResult(t, done)
						terminal := queryLockErrorOutcome(t, out, true, "id", []int64{1})
						referenceRowTimeoutDiagnostic(t, terminal, true, "57014", "canceling statement due to user request", "locking")
					} else if cause == "caller" {
						ctx, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
						out = referenceRowQuery(ctx, reader, q, prepared)
						cancel()
						terminal := queryLockErrorOutcome(t, out, false, "id", nil)
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
						referenceLockExec(t, p.ctx, reader, "SET LOCAL "+setting+"='30ms'", 0)
						out = referenceRowQuery(p.ctx, reader, q, prepared)
						terminal := queryLockErrorOutcome(t, out, b.pg, "id", []int64{1})
						referenceRowTimeoutDiagnostic(t, terminal, b.pg, state, "canceling statement due to "+cause, "locking")
					}
					if err := reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Same physical owner immediately recovers; prefix is free, holder unaffected.
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					queryLockRows(t, p, b, peer, "SELECT id FROM t ORDER BY id FOR UPDATE SKIP LOCKED", true, "id", 1, 3, 4)
					queryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "t")
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id<3", false, 2)
				})
			})
		}
	}
	// Savepoint restores preexisting contributions but discards acquired-prefix
	// contributions from the failed statement. Root units cover waiter journals.
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		holder := queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
		reader := queryLockBegin(t, p.ctx, peer)
		queryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=4 FOR SHARE", false, "id", 4)
		referenceLockExec(t, p.ctx, reader, "SAVEPOINT s", 0)
		queryLockNowait(t, p, b, reader, "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE NOWAIT", true, "t")
		referenceLockExec(t, p.ctx, reader, "ROLLBACK TO SAVEPOINT s", 0)
		c, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		referenceRowAffected(t, p.ctx, c, "UPDATE t SET v=v WHERE id=1", false, 1)
		queryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=4 FOR UPDATE NOWAIT", false, "t")
		queryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=4 FOR SHARE NOWAIT", true, "id", 4)
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRowAffected(t, p.ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
	})
}
