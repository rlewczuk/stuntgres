package reference_test

import (
	"context"
	"testing"
)

func TestReferenceRelationLockWaitsTimeoutPosition(t *testing.T) {
	for _, tc := range []struct{ query, position string }{
		{"SELECT v FROM t", "15"},
		{"/*é*/ SELECT v FROM public.t", "21"},
		{"INSERT INTO t VALUES(2)", "13"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, backend.db)
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					ap, bp := 0, 0
					if backend.pg {
						ap = referenceLockPID(t, ctx, a)
						bp = referenceLockPID(t, ctx, b)
					}
					cached, err := b.PrepareContext(ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					// Observe the actual PostgreSQL lock queue before collecting
					// the timeout; the same bound applies to all three phases.
					referenceLockExec(t, ctx, b, "SET lock_timeout='100ms'", 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					referenceLockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					for _, phase := range []string{"direct", "prepare", "cached"} {
						await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							switch phase {
							case "prepare":
								stmt, err := b.PrepareContext(ctx, tc.query)
								return relationOutcome{stmt: stmt, err: err}
							case "cached":
								result, err := cached.ExecContext(ctx)
								return relationOutcome{result: result, err: err}
							default:
								result, err := b.ExecContext(ctx, tc.query)
								return relationOutcome{result: result, err: err}
							}
						})
						if backend.pg {
							mode := "AccessShareLock"
							if tc.position == "13" {
								mode = "RowExclusiveLock"
							}
							referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "public.t"), mode)
						}
						o := await()
						if o.stmt != nil {
							o.stmt.Close()
							t.Fatal("failed Prepare returned handle")
						}
						if o.result != nil {
							t.Fatal("failed wait returned result", o.result)
						}
						position := tc.position
						if phase == "cached" {
							position = ""
						}
						referenceWaitsDiagnostic(t, o.err, "55P03", "canceling statement due to lock timeout", position)
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					rows, err := b.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg)
				})
			}
		})
	}
}
