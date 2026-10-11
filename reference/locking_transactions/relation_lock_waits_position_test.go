package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationLockWaitsTimeoutPosition(t *testing.T) {
	for _, tc := range []struct{ query, position string }{
		{"SELECT v FROM t", "15"},
		{"/*é*/ SELECT v FROM public.t", "21"},
		{"INSERT INTO t VALUES(2)", "13"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := p.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					ap, bp := 0, 0
					if backend.PG {
						ap = fixture.LockPID(t, ctx, a)
						bp = fixture.LockPID(t, ctx, b)
					}
					cached, err := b.PrepareContext(ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					// Observe the actual PostgreSQL lock queue before collecting
					// the timeout; the same bound applies to all three phases.
					fixture.LockExec(t, ctx, b, "SET lock_timeout='100ms'", 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.LockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					for _, phase := range []string{"direct", "prepare", "cached"} {
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							switch phase {
							case "prepare":
								stmt, err := b.PrepareContext(ctx, tc.query)
								return fixture.RelationOutcome{Stmt: stmt, Err: err}
							case "cached":
								result, err := cached.ExecContext(ctx)
								return fixture.RelationOutcome{Result: result, Err: err}
							default:
								result, err := b.ExecContext(ctx, tc.query)
								return fixture.RelationOutcome{Result: result, Err: err}
							}
						})
						if backend.PG {
							mode := "AccessShareLock"
							if tc.position == "13" {
								mode = "RowExclusiveLock"
							}
							fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "public.t"), mode)
						}
						o := await()
						if o.Stmt != nil {
							o.Stmt.Close()
							t.Fatal("failed Prepare returned handle")
						}
						if o.Result != nil {
							t.Fatal("failed wait returned result", o.Result)
						}
						position := tc.position
						if phase == "cached" {
							position = ""
						}
						fixture.WaitsDiagnostic(t, o.Err, "55P03", "canceling statement due to lock timeout", position)
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					rows, err := b.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG)
				})
			}
		})
	}
}
