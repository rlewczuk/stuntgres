//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceRowLockCancellation(t *testing.T) {
	p := newReferencePair(t)
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
	referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
	ap, bp := referenceRowPIDs(t, p.ctx, a, b)
	holder, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	reader, err := b.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t FOR UPDATE", false), 10)
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	done := referenceRowAsync(t, ctx, reader, "SELECT v FROM t FOR KEY SHARE", true, true)
	referenceRowWait(t, ctx, bp, ap)
	var canceled bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
		t.Fatal(canceled, err)
	}
	outcome := referenceLockResult(t, done)
	if outcome.rows != nil {
		outcome.rows.Close()
		t.Fatal("administration cancellation exposed rows")
	}
	if outcome.stmt != nil {
		outcome.stmt.Close()
	}
	// PostgreSQL administration signal is distinct from native context identity.
	referenceRowTimeoutDiagnostic(t, outcome.err, true, "57014", "canceling statement due to user request", "locking")
	referenceRunStep(t, p.ctx, reader, p.postgresRecord, referenceImplicitError("SELECT v FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
	reader.Rollback()
	holder.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t FOR SHARE", false), 10)
}
func TestReferenceRowLockBoundaries(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,10)", 1))
	for _, suffix := range []string{"FOR UPDATE OF t", "FOR SHARE NOWAIT", "FOR KEY SHARE SKIP LOCKED", "FOR UPDATE FOR SHARE", "FOR UPDATE garbage"} {
		for _, prepared := range []bool{false, true} {
			outcome := referenceRowQuery(p.ctx, p.memory, "SELECT v FROM t "+suffix, prepared)
			if outcome.rows != nil {
				outcome.rows.Close()
				t.Fatal("excluded native clause exposed cursor", suffix)
			}
			if outcome.stmt != nil {
				outcome.stmt.Close()
			}
			if outcome.err == nil {
				t.Fatal("excluded native clause accepted", suffix)
			}
		}
	}
	p.step(t, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	for _, prepared := range []bool{false, true} {
		step := referenceImplicitError("(SELECT id FROM t UNION SELECT id FROM t) FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT"})
		step.selectRows = true
		if prepared {
			step = referencePrepared(step)
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
	}
}
