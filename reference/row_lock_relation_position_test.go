package reference_test

import (
	"context"
	"testing"
)

func TestReferenceRowLockRelationWaitPosition(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, tc := range []struct{ query, position string }{{"SELECT v FROM t ", "15"}, {"/*é*/ SELECT v FROM public.t ", "21"}, {"INSERT INTO destination SELECT v FROM public.t ", "39"}} {
			t.Run(clause+"/"+tc.query, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						referenceLockExec(t, p.ctx, a, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, p.ctx, a, "CREATE TABLE destination(v INT)", 0)
						query := tc.query + clause
						cached, err := b.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer cached.Close()
						referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
						ap, bp := 0, 0
						var oid int64
						if backend.pg {
							ap = referenceLockPID(t, p.ctx, a)
							bp = referenceLockPID(t, p.ctx, b)
							oid = referenceLockOID(t, p.ctx, "public.t")
						}
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						for _, phase := range []string{"direct", "prepare", "cached"} {
							t.Run(phase, func(t *testing.T) {
								await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
									switch phase {
									case "prepare":
										stmt, err := b.PrepareContext(ctx, query)
										return relationOutcome{stmt: stmt, err: err}
									case "cached":
										result, err := cached.ExecContext(ctx)
										return relationOutcome{result: result, err: err}
									default:
										result, err := b.ExecContext(ctx, query)
										return relationOutcome{result: result, err: err}
									}
								})
								if backend.pg {
									referenceLockWaitBehind(t, p.ctx, bp, ap, oid, "RowShareLock")
								}
								out := await()
								if out.stmt != nil {
									out.stmt.Close()
									t.Fatal("failed Prepare returned handle")
								}
								if out.result != nil {
									t.Fatal("failed command returned result")
								}
								position := tc.position
								if phase == "cached" {
									position = ""
								}
								referenceWaitsDiagnostic(t, out.err, "55P03", "canceling statement due to lock timeout", position)
							})
						}
					})
				}
			})
		}
	}
}
