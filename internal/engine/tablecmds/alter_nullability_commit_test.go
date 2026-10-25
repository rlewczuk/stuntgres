package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestAlterNullabilityCombinedCommitBoundary(t *testing.T) {
	setup := func() (*core.Engine, *session.Session, ids.TxRef, ids.ObjectRef) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(NULL,1),(7,2)")
		enginetest.SessionSQL(t, s, "CREATE SEQUENCE seq START 10")
		ref := enginetest.RoleTableRef(t, e, "t")
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "DELETE FROM t WHERE b=1")
		enginetest.SessionSQL(t, s, "UPDATE t SET a=8 WHERE b=2")
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(9,3)")
		enginetest.SessionSQL(t, s, "ALTER SEQUENCE seq RESTART 20")
		enginetest.SessionSQL(t, s, "ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL")
		return e, s, tx, ref
	}
	e, s, tx, _ := setup()
	op := enginetest.TransactionOperation(t, s)
	ctx, cancel := context.WithCancel(op.Context())
	counter := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 1 << 30}
	op.Ctx = counter
	if result, err := e.Commit(op, tx); err != nil || !result.Committed {
		t.Fatal(result, err)
	}
	op.Done()
	cancel()
	checks := counter.checks.Load()
	for at := int32(1); at <= checks; at++ {
		e, s, tx, ref := setup()
		before := captureSequenceDeletePublication(e, s, ref.Address)
		op := enginetest.TransactionOperation(t, s)
		ctx, cancel := context.WithCancel(op.Context())
		op.Ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		result, err := e.Commit(op, tx)
		op.Done()
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) || result.Committed {
				t.Fatal(at, result, err)
			}
			before.assertUnchanged(t, e, s)
		} else {
			if !result.Committed || len(e.Catalog.State.MustRelation(ref.Address).Columns) != 1 {
				t.Fatal("partial combined commit", at, result)
			}
			enginetest.TransactionRows(t, s, "SELECT a FROM t ORDER BY a", 8, 9)
			sequenceScalar(t, s, "SELECT nextval('seq')", 20)
		}
		if s.Transactions.State != session.TxIdle {
			enginetest.TransactionEnd(t, s, tx, false)
		}
		enginetest.LockEmpty(t, e)
	}
}
