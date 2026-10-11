package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockRelationWaitPosition(t *testing.T) {
	for _, clause := range fixture.RowClauses {
		for _, tc := range []struct{ query, position string }{{"SELECT v FROM t ", "15"}, {"/*é*/ SELECT v FROM public.t ", "21"}, {"INSERT INTO destination SELECT v FROM public.t ", "39"}} {
			t.Run(clause+"/"+tc.query, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE destination(v INT)", 0)
						query := tc.query + clause
						cached, err := b.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer cached.Close()
						fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
						ap, bp := 0, 0
						var oid int64
						if backend.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							bp = fixture.LockPID(t, p.Ctx, b)
							oid = fixture.LockOID(t, p.Ctx, "public.t")
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						for _, phase := range []string{"direct", "prepare", "cached"} {
							t.Run(phase, func(t *testing.T) {
								await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
									switch phase {
									case "prepare":
										stmt, err := b.PrepareContext(ctx, query)
										return fixture.RelationOutcome{Stmt: stmt, Err: err}
									case "cached":
										result, err := cached.ExecContext(ctx)
										return fixture.RelationOutcome{Result: result, Err: err}
									default:
										result, err := b.ExecContext(ctx, query)
										return fixture.RelationOutcome{Result: result, Err: err}
									}
								})
								if backend.PG {
									fixture.LockWaitBehind(t, p.Ctx, bp, ap, oid, "RowShareLock")
								}
								out := await()
								if out.Stmt != nil {
									out.Stmt.Close()
									t.Fatal("failed Prepare returned handle")
								}
								if out.Result != nil {
									t.Fatal("failed command returned result")
								}
								position := tc.position
								if phase == "cached" {
									position = ""
								}
								fixture.WaitsDiagnostic(t, out.Err, "55P03", "canceling statement due to lock timeout", position)
							})
						}
					})
				}
			})
		}
	}
}
