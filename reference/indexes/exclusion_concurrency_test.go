package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceExclusionConcurrency(t *testing.T) {
	for _, operation := range []string{"insert", "delete", "update"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%t", operation, commit), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
					run(a, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
					if operation != "insert" {
						run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					}
					run(a, fixture.Exec("BEGIN", 0))
					switch operation {
					case "insert":
						run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					case "delete":
						run(a, fixture.Exec("DELETE FROM t", 1))
					case "update":
						run(a, fixture.Exec("UPDATE t SET v=2", 1))
					}
					run(peer, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(9)", 1)))
					var ap, bp int
					if b.PG {
						if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
					} else {
						run(peer, fixture.Exec("SET lock_timeout='100ms'", 0))
						run(peer, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
						run(peer, fixture.Exec("SET lock_timeout=0", 0))
					}
					done := fixture.RowAsync(t, p.Ctx, peer, "INSERT INTO t VALUES(1)", true, false)
					if b.PG {
						fixture.RowWait(t, p.Ctx, bp, ap)
					}
					command := "ROLLBACK"
					if commit {
						command = "COMMIT"
					}
					run(a, fixture.Exec(command, 0))
					out := fixture.LockResult(t, done)
					if (operation == "insert") == commit {
						want := fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")
						fixture.AssertDiagnostic(t, out.Err, &want)
					} else {
						fixture.State(t, out.Err, "")
						fixture.LockAffected(t, out.Result, 1)
					}
					values := []int64{1, 9}
					if operation == "update" && commit {
						values = []int64{1, 2, 9}
					}
					run(a, fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", values...))
				})
			})
		}
	}
}

func TestReferenceExclusionConcurrencyDeferred(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("initially_immediate=%t", immediate), func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
				mode := "INITIALLY DEFERRED"
				if immediate {
					mode = "INITIALLY IMMEDIATE"
				}
				run(a, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE "+mode+")", 0))
				run(a, fixture.Exec("BEGIN", 0))
				run(peer, fixture.Exec("BEGIN", 0))
				run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
				var ap, bp int
				if b.PG {
					if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
						t.Fatal(err)
					}
					if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
						t.Fatal(err)
					}
				}
				insert := fixture.RowAsync(t, p.Ctx, peer, "INSERT INTO t VALUES(1)", true, false)
				if immediate {
					// Deferrable admission is nonblocking, but its statement-end
					// IMMEDIATE recheck waits for the peer outcome.
					if b.PG {
						fixture.RowWait(t, p.Ctx, bp, ap)
					}
					run(a, fixture.Exec("ROLLBACK", 0))
					out := fixture.LockResult(t, insert)
					fixture.State(t, out.Err, "")
					fixture.LockAffected(t, out.Result, 1)
					run(peer, fixture.Exec("COMMIT", 0))
				} else {
					// Both INSERT calls return before either COMMIT begins.
					out := fixture.LockResult(t, insert)
					fixture.State(t, out.Err, "")
					fixture.LockAffected(t, out.Result, 1)
					// Only the later peer INSERT encountered a potential conflict
					// and queued a deferred recheck. Its COMMIT waits for A.
					commit := fixture.RowAsync(t, p.Ctx, peer, "COMMIT", false, false)
					if b.PG {
						fixture.RowWait(t, p.Ctx, bp, ap)
					}
					run(a, fixture.Exec("ROLLBACK", 0))
					out = fixture.LockResult(t, commit)
					fixture.State(t, out.Err, "")
					fixture.LockAffected(t, out.Result, 0)
				}
				run(a, fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
}

func TestReferenceExclusionConcurrencySnapshot(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(a, fixture.ExclusionReferenceRows("SELECT v FROM t"))
		run(peer, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(a, fixture.ExclusionReferenceRows("SELECT v FROM t"))
		run(a, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(a, fixture.Exec("ROLLBACK", 0))
		run(a, fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
	})
}

func TestReferenceExclusionAlterAdmission(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(a, fixture.Exec("BEGIN", 0))
		run(a, fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
		// Prepare has no AX lock effect while the reader is active.
		stmt, err := peer.PrepareContext(p.Ctx, "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(peer, fixture.Exec("SET lock_timeout='100ms'", 0))
		held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)"}
		run(held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
		run(peer, fixture.Exec("SET lock_timeout=0", 0))
		run(a, fixture.Exec("ROLLBACK", 0))
		run(held, fixture.Exec(held.SQL, 0))
		run(peer, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
	})
}

func TestReferenceExclusionConcurrencyCancellation(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(a, fixture.Exec("BEGIN", 0))
		run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
		defer cancel()
		_, err := peer.ExecContext(ctx, "INSERT INTO t VALUES(1)")
		// lib/pq reports SQL cancellation, while native context cancellation
		// is a public Go error. Neither may publish a partial row.
		if err == nil {
			t.Fatal("conflicting operation ignored cancellation")
		}
		if b.PG {
			fixture.ExclusionReferenceWaitDiagnostic(t, err, "57014", "canceling statement due to user request", "t", true)
			// lib/pq invalidates this physical connection after request-context
			// cancellation. This is an adapter lifecycle boundary, not failed
			// exclusion cleanup or a reason to weaken the subsequent SQL checks.
			_, reuseErr := peer.ExecContext(p.Ctx, "SELECT 1")
			if !errors.Is(reuseErr, driver.ErrBadConn) {
				t.Fatalf("canceled PostgreSQL physical peer got %v want driver.ErrBadConn", reuseErr)
			}
			if err := peer.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
				t.Fatal(err)
			}
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			peer = fresh
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("request did not return its context deadline", err)
		}
		run(a, fixture.Exec("ROLLBACK", 0))
		run(peer, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(peer, fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
	})
}
