package reference_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestReferenceUniqueStatementTimeout(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					a, peer := referenceLockConnections(t, p.ctx, b.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
					run(referenceExec("BEGIN", 0))
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					var ap, bp int
					if b.pg {
						if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
					}
					referenceLockExec(t, p.ctx, peer, "SET statement_timeout='500ms'", 0)
					done := referenceRowAsync(t, p.ctx, peer, "INSERT INTO t VALUES(1)", prepared, false)
					if b.pg {
						referenceRowWait(t, p.ctx, bp, ap)
					}
					outcome := referenceLockResult(t, done)
					want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					if b.pg {
						want.Context = `while inserting index tuple (0,2) in relation "u"`
					}
					referenceAssertDiagnostic(t, outcome.err, &want)
					if outcome.result != nil {
						t.Fatal("failed INSERT returned a result", outcome.result)
					}
					referenceLockExec(t, p.ctx, peer, "RESET statement_timeout", 0)
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO t VALUES(9)", 1))
					run(referenceExec("ROLLBACK", 0))
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
					run(referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(9)}}))
				})
			}
		})
	}
}

func TestReferenceUniqueKeyDeadlock(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					a, peer := referenceLockConnections(t, p.ctx, b.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
					namespace := uniqueReferencePublicNamespace(t, p.ctx, b.db, b.pg)
					var indexOID int64
					if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='u' AND relnamespace=$1", namespace).Scan(&indexOID); err != nil || indexOID <= 0 {
						t.Fatal(indexOID, err)
					}
					if b.pg {
						referenceLockExec(t, p.ctx, a, "SET deadlock_timeout='2s'", 0)
						referenceLockExec(t, p.ctx, peer, "SET deadlock_timeout='100ms'", 0)
					}
					run(referenceExec("BEGIN", 0))
					referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO t VALUES(2)", 1))
					var ap, bp int
					var ax, bx string
					if b.pg {
						if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
						if err := a.QueryRowContext(p.ctx, "SELECT pg_current_xact_id()::text").Scan(&ax); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.ctx, "SELECT pg_current_xact_id()::text").Scan(&bx); err != nil {
							t.Fatal(err)
						}
					}
					first := referenceRowAsync(t, p.ctx, a, "INSERT INTO t VALUES(2)", prepared, false)
					if b.pg {
						referenceRowWait(t, p.ctx, ap, bp)
					}
					second := referenceRowAsync(t, p.ctx, peer, "INSERT INTO t VALUES(1)", prepared, false)
					var outcomes [2]relationOutcome
					var initial int
					select {
					case outcomes[0] = <-first:
						initial = 0
					case outcomes[1] = <-second:
						initial = 1
					case <-p.ctx.Done():
						t.Fatal(p.ctx.Err())
					}
					conns := []referenceExecutor{a, peer}
					if initial == 0 {
						outcomes[1] = referenceLockResult(t, second)
					} else {
						outcomes[0] = referenceLockResult(t, first)
					}
					victim := -1
					for i, outcome := range outcomes {
						if outcome.err != nil {
							if victim != -1 {
								t.Fatal("both UNIQUE participants failed", outcomes[0].err, outcomes[1].err)
							}
							victim = i
							if outcome.result != nil {
								t.Fatal("deadlock victim returned a result", outcome.result)
							}
							if b.pg {
								if victim != 1 {
									t.Fatal("PostgreSQL deadlock victim violated configured detector ordering", victim)
								}
								want := controlsReferenceDiagnostic("40P01", "deadlock detected")
								want.Hint = "See server log for query details."
								want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", bp, ax, ap, ap, bx, bp)
								want.Context = `while inserting index tuple (0,4) in relation "u"`
								referenceAssertDiagnostic(t, outcome.err, &want)
							} else {
								uniqueReferenceNativeKeyDeadlock(t, outcome.err, indexOID)
							}
						} else {
							referenceLockAffected(t, outcome.result, 1)
						}
					}
					if victim < 0 {
						t.Fatal("opposite UNIQUE claims did not deadlock")
					}
					// Both outcomes are already complete: abort released the victim's
					// claims without requiring an explicit ROLLBACK.
					referenceRunStep(t, p.ctx, conns[victim], b.recorder, referenceImplicitError("INSERT INTO t VALUES(8)", referenceTransactionAborted()))
					referenceLockExec(t, p.ctx, conns[victim], "ROLLBACK", 0)
					referenceLockExec(t, p.ctx, conns[1-victim], "COMMIT", 0)
					referenceRunStep(t, p.ctx, conns[victim], b.recorder, referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					referenceRunStep(t, p.ctx, conns[victim], b.recorder, referenceExec("INSERT INTO t VALUES(3)", 1))
					run(referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					run(referenceExec("BEGIN", 0))
					run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(referenceExec("ROLLBACK", 0))
				})
			}
		})
	}
}

func uniqueReferenceNativeKeyDeadlock(t *testing.T, err error, indexOID int64) {
	t.Helper()
	d := referenceExtractDiagnostic(t, err)
	lines := strings.Split(d.Detail, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly two native key edges: %q", d.Detail)
	}
	pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for UniqueKey on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\) unique key; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	a, b := pattern.FindStringSubmatch(lines[0]), pattern.FindStringSubmatch(lines[1])
	if a == nil || b == nil || a[1] == b[1] || a[2] == b[2] || a[1] != b[6] || b[1] != a[6] || a[2] != b[7] || b[2] != a[7] || a[3] != b[8] || b[3] != a[8] || a[3] != b[3] {
		t.Fatalf("complete native closed key cycle: %q", d.Detail)
	}
	for _, edge := range [][]string{a, b} {
		if edge[4] != fmt.Sprint(indexOID) || edge[5] != edge[3] {
			t.Fatalf("actual backing identity/epoch: %q", d.Detail)
		}
	}
	// Only real opaque transaction/session identities vary. All logical fields
	// and the entire key-resource edge grammar above remain mandatory.
	d.Detail = ""
	want := controlsReferenceDiagnostic("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("complete native key deadlock: got %#v want %#v", d, want)
	}
}
