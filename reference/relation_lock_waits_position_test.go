//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceRelationLockWaitsTimeoutPosition(t *testing.T) {
	for _, tc := range []struct{ query, position string }{
		{"SELECT v FROM t", "15"},
		{"/*é*/ SELECT v FROM public.t", "21"},
		{"INSERT INTO t VALUES(2)", "13"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferencePair(t)
			for index, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[index], func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, db)
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					ap, bp := 0, 0
					if index == 1 {
						ap = referenceLockPID(t, ctx, a)
						bp = referenceLockPID(t, ctx, b)
					}
					cached, err := b.PrepareContext(ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					referenceLockExec(t, ctx, b, "SET lock_timeout='200ms'", 0)
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
						if index == 1 {
							mode := "AccessShareLock"
							if tc.position == "13" {
								mode = "RowExclusiveLock"
							}
							referenceLockWaitBehind(t, ctx, bp, ap, mode)
						}
						o := await()
						if o.stmt != nil {
							o.stmt.Close()
							t.Fatal("failed Prepare returned handle")
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
					referenceTransactionValues(t, rows)
				})
			}
		})
	}
}
