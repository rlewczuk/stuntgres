package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestCommentLocks(t *testing.T) {
	for _, q := range []string{"COMMENT ON TABLE t IS 'new'", "COMMENT ON COLUMN t.v IS 'new'"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "COMMENT ON TABLE t IS 'old'")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			enginetest.LockAwait(t, enginetest.LockAsync(e, b, context.Background(), q, true), nil)
			ctx, cancel := context.WithCancel(context.Background())
			waiting := enginetest.LockAsync(e, b, ctx, q, false)
			enginetest.LockPending(t, e, 1)
			cancel()
			enginetest.LockAwait(t, waiting, context.Canceled)
			commentWant(t, e, e.CatalogRead(), ref, "old")
			enginetest.LockPending(t, e, 0)
			waiting = enginetest.LockAsync(e, b, context.Background(), q, false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockAwait(t, waiting, nil)
			enginetest.LockEmpty(t, e)
			if q == "COMMENT ON TABLE t IS 'new'" {
				commentWant(t, e, e.CatalogRead(), ref, "new")
			} else {
				ref.Address.SubID = 1
				commentWant(t, e, e.CatalogRead(), ref, "new")
			}
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
		})
	}
	t.Run("replacement fence", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		tx := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		waiting := enginetest.LockAsync(e, b, context.Background(), "COMMENT ON TABLE t IS 'stale'", false)
		enginetest.LockPending(t, e, 1)
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		enginetest.LockAwait(t, waiting, ids.ErrCatalogReplaced)
		enginetest.TransactionEnd(t, a, tx, false)
		enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
		commentWant(t, e, e.CatalogRead(), enginetest.CatalogRef(t, e, e.CatalogRead(), "t"), "")
		enginetest.LockEmpty(t, e)
	})
	t.Run("drop recreation fence", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		tx := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waiting := enginetest.LockAsync(e, b, ctx, "COMMENT ON COLUMN t.v IS 'stale'", false)
		enginetest.LockPending(t, e, 1)
		enginetest.SessionSQL(t, a, "DROP TABLE t")
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		enginetest.TransactionEnd(t, a, tx, true)
		enginetest.LockAwait(t, waiting, ids.ErrCatalogReplaced)
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		ref.Address.SubID = 1
		commentWant(t, e, e.CatalogRead(), ref, "")
		enginetest.LockEmpty(t, e)
	})
}
