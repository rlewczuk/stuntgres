package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterNullabilityCombinedCommitBoundary(t *testing.T) {
	setup := func() (*Engine, *Session, ids.TxRef, ids.ObjectRef) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(NULL,1),(7,2)")
		sessionSQL(t, s, "CREATE SEQUENCE seq START 10")
		ref := roleTableRef(t, e, "t")
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "DELETE FROM t WHERE b=1")
		sessionSQL(t, s, "UPDATE t SET a=8 WHERE b=2")
		sessionSQL(t, s, "INSERT INTO t VALUES(9,3)")
		sessionSQL(t, s, "ALTER SEQUENCE seq RESTART 20")
		sessionSQL(t, s, "ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL")
		return e, s, tx, ref
	}
	e, s, tx, _ := setup()
	op := transactionOperation(t, s)
	ctx, cancel := context.WithCancel(op.Context())
	counter := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 1 << 30}
	op.ctx = counter
	if result, err := e.Commit(op, tx); err != nil || !result.Committed {
		t.Fatal(result, err)
	}
	op.Done()
	cancel()
	checks := counter.checks.Load()
	for at := int32(1); at <= checks; at++ {
		e, s, tx, ref := setup()
		before := captureSequenceDeletePublication(e, s, ref.Address)
		op := transactionOperation(t, s)
		ctx, cancel := context.WithCancel(op.Context())
		op.ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		result, err := e.Commit(op, tx)
		op.Done()
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) || result.Committed {
				t.Fatal(at, result, err)
			}
			before.assertUnchanged(t, e, s)
		} else {
			if !result.Committed || len(e.catalog.state.MustRelation(ref.Address).columns) != 1 {
				t.Fatal("partial combined commit", at, result)
			}
			transactionRows(t, s, "SELECT a FROM t ORDER BY a", 8, 9)
			sequenceScalar(t, s, "SELECT nextval('seq')", 20)
		}
		if s.transactions.state != TxIdle {
			transactionEnd(t, s, tx, false)
		}
		lockEmpty(t, e)
	}
}
