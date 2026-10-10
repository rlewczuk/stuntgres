package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceExclusionConcurrency(t *testing.T) {
	for _, operation := range []string{"insert", "delete", "update"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%t", operation, commit), func(t *testing.T) {
				exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, peer := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
					run(a, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
					if operation != "insert" {
						run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
					}
					run(a, referenceExec("BEGIN", 0))
					switch operation {
					case "insert":
						run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
					case "delete":
						run(a, referenceExec("DELETE FROM t", 1))
					case "update":
						run(a, referenceExec("UPDATE t SET v=2", 1))
					}
					run(peer, referencePrepared(referenceExec("INSERT INTO t VALUES(9)", 1)))
					var ap, bp int
					if b.pg {
						if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
					} else {
						run(peer, referenceExec("SET lock_timeout='100ms'", 0))
						run(peer, referenceErrorDiagnostic("INSERT INTO t VALUES(1)", controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
						run(peer, referenceExec("SET lock_timeout=0", 0))
					}
					done := referenceRowAsync(t, p.ctx, peer, "INSERT INTO t VALUES(1)", true, false)
					if b.pg {
						referenceRowWait(t, p.ctx, bp, ap)
					}
					command := "ROLLBACK"
					if commit {
						command = "COMMIT"
					}
					run(a, referenceExec(command, 0))
					out := referenceLockResult(t, done)
					if (operation == "insert") == commit {
						want := exclusionReferenceConflict("t", "ex", "v", "1", "1")
						referenceAssertDiagnostic(t, out.err, &want)
					} else {
						referenceState(t, out.err, "")
						referenceLockAffected(t, out.result, 1)
					}
					values := []int64{1, 9}
					if operation == "update" && commit {
						values = []int64{1, 2, 9}
					}
					run(a, exclusionReferenceRows("SELECT v FROM t ORDER BY v", values...))
				})
			})
		}
	}
}

func TestReferenceExclusionConcurrencyDeferred(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("initially_immediate=%t", immediate), func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, peer := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
				mode := "INITIALLY DEFERRED"
				if immediate {
					mode = "INITIALLY IMMEDIATE"
				}
				run(a, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE "+mode+")", 0))
				run(a, referenceExec("BEGIN", 0))
				run(peer, referenceExec("BEGIN", 0))
				run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
				var ap, bp int
				if b.pg {
					if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
						t.Fatal(err)
					}
					if err := peer.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
						t.Fatal(err)
					}
				}
				insert := referenceRowAsync(t, p.ctx, peer, "INSERT INTO t VALUES(1)", true, false)
				if immediate {
					// Deferrable admission is nonblocking, but its statement-end
					// IMMEDIATE recheck waits for the peer outcome.
					if b.pg {
						referenceRowWait(t, p.ctx, bp, ap)
					}
					run(a, referenceExec("ROLLBACK", 0))
					out := referenceLockResult(t, insert)
					referenceState(t, out.err, "")
					referenceLockAffected(t, out.result, 1)
					run(peer, referenceExec("COMMIT", 0))
				} else {
					// Both INSERT calls return before either COMMIT begins.
					out := referenceLockResult(t, insert)
					referenceState(t, out.err, "")
					referenceLockAffected(t, out.result, 1)
					// Only the later peer INSERT encountered a potential conflict
					// and queued a deferred recheck. Its COMMIT waits for A.
					commit := referenceRowAsync(t, p.ctx, peer, "COMMIT", false, false)
					if b.pg {
						referenceRowWait(t, p.ctx, bp, ap)
					}
					run(a, referenceExec("ROLLBACK", 0))
					out = referenceLockResult(t, commit)
					referenceState(t, out.err, "")
					referenceLockAffected(t, out.result, 0)
				}
				run(a, exclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
}

func TestReferenceExclusionConcurrencySnapshot(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(a, referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(a, exclusionReferenceRows("SELECT v FROM t"))
		run(peer, referenceExec("INSERT INTO t VALUES(1)", 1))
		run(a, exclusionReferenceRows("SELECT v FROM t"))
		run(a, referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(a, referenceExec("ROLLBACK", 0))
		run(a, exclusionReferenceRows("SELECT v FROM t", 1))
	})
}

func TestReferenceExclusionAlterAdmission(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE t(v INT)", 0))
		run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
		run(a, referenceExec("BEGIN", 0))
		run(a, exclusionReferenceRows("SELECT v FROM t", 1))
		// Prepare has no AX lock effect while the reader is active.
		stmt, err := peer.PrepareContext(p.ctx, "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(peer, referenceExec("SET lock_timeout='100ms'", 0))
		held := referenceHeldStatement{referenceExecutor: peer, statement: stmt, query: "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)"}
		run(held, referenceErrorDiagnostic(held.query, controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
		run(peer, referenceExec("SET lock_timeout=0", 0))
		run(a, referenceExec("ROLLBACK", 0))
		run(held, referenceExec(held.query, 0))
		run(peer, referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
	})
}

func TestReferenceExclusionConcurrencyCancellation(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(a, referenceExec("BEGIN", 0))
		run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
		ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
		defer cancel()
		_, err := peer.ExecContext(ctx, "INSERT INTO t VALUES(1)")
		// lib/pq reports SQL cancellation, while native context cancellation
		// is a public Go error. Neither may publish a partial row.
		if err == nil {
			t.Fatal("conflicting operation ignored cancellation")
		}
		if b.pg {
			exclusionReferenceWaitDiagnostic(t, err, "57014", "canceling statement due to user request", true)
			// lib/pq invalidates this physical connection after request-context
			// cancellation. This is an adapter lifecycle boundary, not failed
			// exclusion cleanup or a reason to weaken the subsequent SQL checks.
			_, reuseErr := peer.ExecContext(p.ctx, "SELECT 1")
			if !errors.Is(reuseErr, driver.ErrBadConn) {
				t.Fatalf("canceled PostgreSQL physical peer got %v want driver.ErrBadConn", reuseErr)
			}
			if err := peer.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
				t.Fatal(err)
			}
			fresh, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			peer = fresh
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("request did not return its context deadline", err)
		}
		run(a, referenceExec("ROLLBACK", 0))
		run(peer, referenceExec("INSERT INTO t VALUES(1)", 1))
		run(peer, exclusionReferenceRows("SELECT v FROM t", 1))
	})
}
