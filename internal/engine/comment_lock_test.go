package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCommentLocks(t *testing.T) {
	for _, q := range []string{"COMMENT ON TABLE t IS 'new'", "COMMENT ON COLUMN t.v IS 'new'"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, a, "COMMENT ON TABLE t IS 'old'")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			lockAwait(t, lockAsync(e, b, context.Background(), q, true), nil)
			ctx, cancel := context.WithCancel(context.Background())
			waiting := lockAsync(e, b, ctx, q, false)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, waiting, context.Canceled)
			commentWant(t, e, e.CatalogRead(), ref, "old")
			lockPending(t, e, 0)
			waiting = lockAsync(e, b, context.Background(), q, false)
			lockPending(t, e, 1)
			transactionEnd(t, a, tx, false)
			lockAwait(t, waiting, nil)
			lockEmpty(t, e)
			if q == "COMMENT ON TABLE t IS 'new'" {
				commentWant(t, e, e.CatalogRead(), ref, "new")
			} else {
				ref.Address.SubID = 1
				commentWant(t, e, e.CatalogRead(), ref, "new")
			}
			sessionSQL(t, b, "INSERT INTO t VALUES(7)")
		})
	}
	t.Run("replacement fence", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		waiting := lockAsync(e, b, context.Background(), "COMMENT ON TABLE t IS 'stale'", false)
		lockPending(t, e, 1)
		if err := e.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		lockAwait(t, waiting, ids.ErrCatalogReplaced)
		transactionEnd(t, a, tx, false)
		sessionSQL(t, b, "CREATE TABLE t(v INT)")
		commentWant(t, e, e.CatalogRead(), catalogRef(t, e, e.CatalogRead(), "t"), "")
		lockEmpty(t, e)
	})
	t.Run("drop recreation fence", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waiting := lockAsync(e, b, ctx, "COMMENT ON COLUMN t.v IS 'stale'", false)
		lockPending(t, e, 1)
		sessionSQL(t, a, "DROP TABLE t")
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		transactionEnd(t, a, tx, true)
		lockAwait(t, waiting, ids.ErrCatalogReplaced)
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		ref.Address.SubID = 1
		commentWant(t, e, e.CatalogRead(), ref, "")
		lockEmpty(t, e)
	})
}
