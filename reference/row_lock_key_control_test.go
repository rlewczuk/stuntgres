//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceRowLockKeyControl(t *testing.T) {
	p := newReferencePair(t)
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0)
	referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
	ap, bp := referenceRowPIDs(t, p.ctx, a, b)
	tx, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t FOR KEY SHARE", false), 10)
	// PostgreSQL-only unique-key metadata control; native tables are keyless.
	referenceLockExec(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", 1)
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	done := referenceRowAsync(t, ctx, b, "UPDATE t SET id=2 WHERE id=1", true, false)
	referenceRowWait(t, ctx, bp, ap)
	tx.Rollback()
	outcome := referenceLockResult(t, done)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	referenceLockAffected(t, outcome.result, 1)
	referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", true), 20)
}
