package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModeCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := "implicit"
			if explicit {
				name = "explicit"
			}
			if prepared {
				name += "-held"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
						triggerModeSetup(t, p, b, c)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						q := "ALTER TABLE tm_data DISABLE TRIGGER a"
						var x fixture.Executor = c
						if prepared {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						pid, hpid := 0, 0
						var oid int64
						if b.PG {
							pid = fixture.LockPID(t, p.Ctx, c)
							hpid = fixture.LockPID(t, p.Ctx, holder)
							oid = fixture.LockOID(t, p.Ctx, "tm_data")
						}
						if explicit {
							run(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(context.Background(), "ROLLBACK")
						}
						htx := fixture.QueryLockBegin(t, p.Ctx, holder)
						defer htx.Rollback()
						fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec("LOCK TABLE tm_data IN SHARE MODE", 0))
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						done := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(context.Context) fixture.RelationOutcome {
							defer close(done)
							r, err := x.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						joined := false
						defer func() {
							cancel()
							if !joined {
								join()
							}
						}()
						fixture.RelationLockPending(t, p, b, pid, hpid, oid, "ShareRowExclusiveLock", done)
						if b.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
						out := join()
						joined = true
						if b.PG {
							want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							fixture.AssertDiagnostic(t, out.Err, &want)
						} else if !errors.Is(out.Err, context.Canceled) {
							t.Fatal(out.Err)
						}
						if explicit {
							run(fixture.Exec("ROLLBACK", 0))
						}
						if err := htx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(triggerModeCatalog("tm_data", []any{"a", "O"}))
						run(triggerRows("INSERT INTO tm_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(105)}))
					})
				}
			})
		}
	}
}
