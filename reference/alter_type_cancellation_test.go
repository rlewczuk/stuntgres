//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceAlterTypeCancellation(t *testing.T) {
	p := newReferencePair(t)
	for _, prepared := range []bool{false, true} {
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE typecancel(c INT)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO typecancel VALUES(7)", 1)
		q := "ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1"
		var stmt *sql.Stmt
		var err error
		if prepared {
			stmt, err = b.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
		}
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		referenceLockExec(t, p.ctx, holder, "LOCK TABLE typecancel IN ACCESS EXCLUSIVE MODE", 0)
		var pid int
		if err = b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		done := make(chan relationOutcome, 1)
		go func() {
			o := relationOutcome{}
			if prepared {
				o.result, o.err = stmt.ExecContext(p.ctx)
			} else {
				o.result, o.err = b.ExecContext(p.ctx, q)
			}
			done <- o
		}()
		referenceLockWait(t, p.ctx, pid)
		var canceled bool
		if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		outcome := referenceLockResult(t, done)
		referenceAssertDiagnostic(t, outcome.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		if stmt != nil {
			stmt.Close()
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT c FROM typecancel", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
		referenceLockExec(t, p.ctx, b, q, 0)
		referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT c FROM typecancel", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
		referenceLockExec(t, p.ctx, b, "DROP TABLE typecancel", 0)
		a.Close()
		b.Close()
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE typecancel(c INT)", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO typecancel VALUES(7)", 1))
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if _, err := p.memory.ExecContext(ctx, "ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT c FROM typecancel", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT c FROM typecancel", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
}
