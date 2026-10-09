package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceUpdateReturningCancellation(t *testing.T) {
	for _, kind := range []string{"admin", "lock_timeout", "statement_timeout"} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name+"/"+kind, func(t *testing.T) {
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
				var physical string
				var ap, bp int
				if backend.pg {
					var tuple string
					if err := b.QueryRowContext(p.ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
						t.Fatal(err)
					}
					physical = "while updating tuple " + tuple + ` in relation "t"`
					ap, bp = referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
				}
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
				if !backend.pg {
					ctx, cancel := context.WithTimeout(p.ctx, 25*time.Millisecond)
					defer cancel()
					rows, err := b.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING old.id,new.id")
					if rows != nil {
						rows.Close()
						t.Fatal("canceled eager output exposed rows")
					}
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal(err)
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					deleteReferenceRows(t, p, b, backend.recorder, 1, 2)
					return
				}
				if kind != "admin" {
					referenceLockExec(t, p.ctx, b, "SET "+kind+"='500ms'", 0)
				}
				await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					rows, err := b.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING old.id,new.id")
					return relationOutcome{rows: rows, err: err}
				})
				updateFromPGWait(t, p.ctx, bp)
				var exactBlocker bool
				if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT $2=ANY(pg_blocking_pids($1))", bp, ap).Scan(&exactBlocker); err != nil || !exactBlocker {
					t.Fatal("unexpected tuple blocker", exactBlocker, err)
				}
				var earlierDeletion bool
				if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND granted AND transactionid=(SELECT xmax FROM t WHERE id=1))", bp).Scan(&earlierDeletion); err != nil || !earlierDeletion {
					t.Fatal("wait lacked independently observed earlier tuple deletion", earlierDeletion, err)
				}
				if kind == "admin" {
					var canceled bool
					if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				}
				o := await()
				if o.rows != nil {
					o.rows.Close()
					t.Fatal("failed RETURNING exposed cursor")
				}
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: physical}
				if kind == "lock_timeout" {
					want.Code, want.Message = "55P03", "canceling statement due to lock timeout"
				}
				if kind == "statement_timeout" {
					want.Message = "canceling statement due to statement timeout"
				}
				referenceAssertDiagnostic(t, o.err, &want)
				if err = holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceLockExec(t, p.ctx, b, "SET lock_timeout=0", 0)
				referenceLockExec(t, p.ctx, b, "SET statement_timeout=0", 0)
				deleteReferenceRows(t, p, b, backend.recorder, 1, 2)
			})
		}
	}
}
