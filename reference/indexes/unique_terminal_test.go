package reference_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueStatementTimeout(t *testing.T) {
	uniqueReferenceStatementTimeout(t, false)
}

func TestReferenceUniqueIndexStatementTimeout(t *testing.T) {
	uniqueReferenceStatementTimeout(t, true)
}

func uniqueReferenceStatementTimeout(t *testing.T, standalone bool) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					var ap, bp int
					if b.PG {
						if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
					}
					fixture.LockExec(t, p.Ctx, peer, "SET statement_timeout='500ms'", 0)
					done := fixture.RowAsync(t, p.Ctx, peer, "INSERT INTO t VALUES(1)", prepared, false)
					if b.PG {
						fixture.RowWait(t, p.Ctx, bp, ap)
					}
					outcome := fixture.LockResult(t, done)
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					if b.PG {
						want.Context = `while inserting index tuple (0,2) in relation "u"`
					}
					fixture.AssertDiagnostic(t, outcome.Err, &want)
					if outcome.Result != nil {
						t.Fatal("failed INSERT returned a result", outcome.Result)
					}
					fixture.LockExec(t, p.Ctx, peer, "RESET statement_timeout", 0)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(9)", 1))
					run(fixture.Exec("ROLLBACK", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
					run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(9)}}))
				})
			}
		})
	}
}

func TestReferenceUniqueKeyDeadlock(t *testing.T) {
	uniqueReferenceKeyDeadlock(t, false)
}

func TestReferenceUniqueIndexKeyDeadlock(t *testing.T) {
	uniqueReferenceKeyDeadlock(t, true)
}

func uniqueReferenceKeyDeadlock(t *testing.T, standalone bool) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
					namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
					var indexOID int64
					if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='u' AND relnamespace=$1", namespace).Scan(&indexOID); err != nil || indexOID <= 0 {
						t.Fatal(indexOID, err)
					}
					if b.PG {
						fixture.LockExec(t, p.Ctx, a, "SET deadlock_timeout='2s'", 0)
						fixture.LockExec(t, p.Ctx, peer, "SET deadlock_timeout='100ms'", 0)
					}
					run(fixture.Exec("BEGIN", 0))
					fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(2)", 1))
					var ap, bp int
					var ax, bx string
					if b.PG {
						if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
							t.Fatal(err)
						}
						if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&ax); err != nil {
							t.Fatal(err)
						}
						if err := peer.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&bx); err != nil {
							t.Fatal(err)
						}
					}
					first := fixture.RowAsync(t, p.Ctx, a, "INSERT INTO t VALUES(2)", prepared, false)
					if b.PG {
						fixture.RowWait(t, p.Ctx, ap, bp)
					}
					second := fixture.RowAsync(t, p.Ctx, peer, "INSERT INTO t VALUES(1)", prepared, false)
					var outcomes [2]fixture.RelationOutcome
					var initial int
					select {
					case outcomes[0] = <-first:
						initial = 0
					case outcomes[1] = <-second:
						initial = 1
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					}
					conns := []fixture.Executor{a, peer}
					if initial == 0 {
						outcomes[1] = fixture.LockResult(t, second)
					} else {
						outcomes[0] = fixture.LockResult(t, first)
					}
					victim := -1
					for i, outcome := range outcomes {
						if outcome.Err != nil {
							if victim != -1 {
								t.Fatal("both UNIQUE participants failed", outcomes[0].Err, outcomes[1].Err)
							}
							victim = i
							if outcome.Result != nil {
								t.Fatal("deadlock victim returned a result", outcome.Result)
							}
							if b.PG {
								if victim != 1 {
									t.Fatal("PostgreSQL deadlock victim violated configured detector ordering", victim)
								}
								want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
								want.Hint = "See server log for query details."
								want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", bp, ax, ap, ap, bx, bp)
								want.Context = `while inserting index tuple (0,4) in relation "u"`
								fixture.AssertDiagnostic(t, outcome.Err, &want)
							} else {
								uniqueReferenceNativeKeyDeadlock(t, outcome.Err, indexOID)
							}
						} else {
							fixture.LockAffected(t, outcome.Result, 1)
						}
					}
					if victim < 0 {
						t.Fatal("opposite UNIQUE claims did not deadlock")
					}
					// Both outcomes are already complete: abort released the victim's
					// claims without requiring an explicit ROLLBACK.
					fixture.RunStep(t, p.Ctx, conns[victim], b.Recorder, fixture.ImplicitError("INSERT INTO t VALUES(8)", fixture.TransactionAborted()))
					fixture.LockExec(t, p.Ctx, conns[victim], "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, conns[1-victim], "COMMIT", 0)
					fixture.RunStep(t, p.Ctx, conns[victim], b.Recorder, fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					fixture.RunStep(t, p.Ctx, conns[victim], b.Recorder, fixture.Exec("INSERT INTO t VALUES(3)", 1))
					run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(fixture.Exec("ROLLBACK", 0))
				})
			}
		})
	}
}

func uniqueReferenceNativeKeyDeadlock(t *testing.T, err error, indexOID int64) {
	t.Helper()
	d := fixture.ExtractDiagnostic(t, err)
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
	want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("complete native key deadlock: got %#v want %#v", d, want)
	}
}
