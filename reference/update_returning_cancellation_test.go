//go:build reference

package reference_test

import (
	"context"
	"strings"
	"testing"
)

func TestReferenceUpdateReturningCancellation(t *testing.T) {
	for _, kind := range []string{"admin", "lock_timeout", "statement_timeout"} {
		t.Run("postgres/"+kind, func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			physical := strings.Replace(deleteReferenceTupleContext(t, p.ctx, b), "deleting", "updating", 1)
			ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
			if kind != "admin" {
				referenceLockExec(t, p.ctx, b, "SET "+kind+"='500ms'", 0)
			}
			await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
				rows, err := b.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING old.id,new.id")
				return relationOutcome{rows: rows, err: err}
			})
			deleteReferenceWaitRow(t, p.ctx, bp, ap)
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
			deleteReferenceRows(t, p, b, p.postgresRecord, 1, 2)
		})
	}
}
