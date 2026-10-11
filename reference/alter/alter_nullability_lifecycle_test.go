package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityLifecycle(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL, DROP CONSTRAINT IF EXISTS missing"} {
		t.Run(q, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
					fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='150ms'", 0)
					holderPID := 0
					if backend.PG {
						holderPID = fixture.LockPID(t, p.Ctx, a)
					}
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					_, err = b.ExecContext(p.Ctx, q)
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
					fixture.AssertDiagnostic(t, err, &want)
					fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
					if backend.PG {
						pid := fixture.LockPID(t, p.Ctx, b)
						oid := fixture.LockOID(t, p.Ctx, "public.t")
						waiter, err := b.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						fixture.LockExec(t, p.Ctx, waiter, "SAVEPOINT recover", 0)
						done := make(chan fixture.RelationOutcome, 1)
						ctx, cancel := context.WithCancel(p.Ctx)
						joined := false
						go func() {
							result, err := waiter.ExecContext(ctx, q)
							done <- fixture.RelationOutcome{Result: result, Err: err}
						}()
						defer func() {
							cancel()
							holder.Rollback()
							if !joined {
								<-done
							}
							waiter.Rollback()
						}()
						fixture.LockWaitBehind(t, p.Ctx, pid, holderPID, oid, "AccessExclusiveLock")
						var canceled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						outcome := fixture.LockResult(t, done)
						joined = true
						want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
						fixture.AssertDiagnostic(t, outcome.Err, &want)
						fixture.RunStep(t, p.Ctx, waiter, backend.Recorder, fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						fixture.LockExec(t, p.Ctx, waiter, "ROLLBACK TO recover", 0)
						waiter.Rollback()
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Exec(q, 0))
				})
			}
		})
	}
}
