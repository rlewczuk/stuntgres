package tablecmds_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAlterRenameLock(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		for _, mode := range []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.AccessExclusive} {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			ref := enginetest.RoleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			tx := enginetest.TransactionBegin(t, a)
			if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
			done := enginetest.LockAsync(e, b, context.Background(), q, true)
			enginetest.LockAwait(t, done, nil)
			enginetest.LockPending(t, e, 0)
			ctx, cancel := context.WithCancel(context.Background())
			done = enginetest.LockAsync(e, b, ctx, q, false)
			enginetest.LockPending(t, e, 1)
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
			if e.Catalog.State.Get(ref.Address) != old {
				t.Fatal("canceled rename changed descriptor")
			}
			done = enginetest.LockAsync(e, b, context.Background(), q, false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockAwait(t, done, nil)
			enginetest.LockEmpty(t, e)
		}
	}
}

func TestAlterRenameLockGrantTime(t *testing.T) {
	for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SCHEMA shadow")
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
			if kind == "shadow" {
				enginetest.SessionSQL(t, b, "SET search_path TO shadow,public")
			}
			ref := enginetest.RoleTableRef(t, e, "t")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "ALTER TABLE t RENAME a TO renamed", false)
			enginetest.LockPending(t, e, 1)
			switch kind {
			case "revision":
				stage, ref, _ := notNullMetadataStage(t, e)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: ref.Epoch}); err != nil {
					t.Fatal(err)
				}
			case "shadow":
				enginetest.Run(t, e, "CREATE TABLE shadow.t(a INT)")
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(a INT);INSERT INTO t VALUES(91)")); err != nil {
					t.Fatal(err)
				}
			case "recreate":
				enginetest.SessionSQL(t, a, "DROP TABLE t")
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			}
			if kind == "reset" || kind == "restore" {
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, a, tx, false)
			} else {
				enginetest.TransactionEnd(t, a, tx, kind == "recreate")
				if kind == "revision" {
					enginetest.LockAwait(t, done, nil)
					d := e.Catalog.State.MustRelation(ref.Address)
					if d.Columns[0].Name != "renamed" || len(d.Constraints.NotNull) != 1 {
						t.Fatal(d)
					}
				} else {
					enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAlterRenameLockTimeout(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT)")
			enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
			ref := enginetest.RoleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			done := waitsCommandAsync(e, b, context.Background(), q, false)
			enginetest.LockPending(t, e, 1)
			select {
			case err := <-done:
				renameWantError(t, err, "55P03", "canceling statement due to lock timeout")
			case <-time.After(5 * time.Second):
				t.Fatal("rename timeout stuck")
			}
			if e.Catalog.State.Get(ref.Address) != old {
				t.Fatal("timeout mutated descriptor")
			}
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
