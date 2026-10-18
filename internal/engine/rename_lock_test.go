package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAlterRenameLock(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		for _, mode := range []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.AccessExclusive} {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(a INT)")
			ref := roleTableRef(t, e, "t")
			old := e.catalog.state.Get(ref.Address)
			tx := transactionBegin(t, a)
			if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
			done := lockAsync(e, b, context.Background(), q, true)
			lockAwait(t, done, nil)
			lockPending(t, e, 0)
			ctx, cancel := context.WithCancel(context.Background())
			done = lockAsync(e, b, ctx, q, false)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, done, context.Canceled)
			if e.catalog.state.Get(ref.Address) != old {
				t.Fatal("canceled rename changed descriptor")
			}
			done = lockAsync(e, b, context.Background(), q, false)
			lockPending(t, e, 1)
			transactionEnd(t, a, tx, false)
			lockAwait(t, done, nil)
			lockEmpty(t, e)
		}
	}
}

func TestAlterRenameLockGrantTime(t *testing.T) {
	for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SCHEMA shadow")
			sessionSQL(t, a, "CREATE TABLE t(a INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7)")
			if kind == "shadow" {
				sessionSQL(t, b, "SET search_path TO shadow,public")
			}
			ref := roleTableRef(t, e, "t")
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			done := lockAsync(e, b, context.Background(), "ALTER TABLE t RENAME a TO renamed", false)
			lockPending(t, e, 1)
			switch kind {
			case "revision":
				stage, ref, _ := notNullMetadataStage(t, e)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
					t.Fatal(err)
				}
			case "shadow":
				run(t, e, "CREATE TABLE shadow.t(a INT)")
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(a INT);INSERT INTO t VALUES(91)")); err != nil {
					t.Fatal(err)
				}
			case "recreate":
				run(t, e, "DROP TABLE t")
				run(t, e, "CREATE TABLE t(a INT)")
			}
			if kind == "reset" || kind == "restore" {
				lockAwait(t, done, ids.ErrCatalogReplaced)
				transactionEnd(t, a, tx, false)
			} else {
				transactionEnd(t, a, tx, false)
				if kind == "revision" {
					lockAwait(t, done, nil)
					d := e.catalog.state.MustRelation(ref.Address)
					if d.Columns[0].Name != "renamed" || len(d.NotNullConstraints) != 1 {
						t.Fatal(d)
					}
				} else {
					lockAwait(t, done, ids.ErrCatalogReplaced)
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestAlterRenameLockTimeout(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(a INT)")
			sessionSQL(t, b, "SET lock_timeout='80ms'")
			ref := roleTableRef(t, e, "t")
			old := e.catalog.state.Get(ref.Address)
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			done := waitsCommandAsync(e, b, context.Background(), q, false)
			lockPending(t, e, 1)
			select {
			case err := <-done:
				renameWantError(t, err, "55P03", "canceling statement due to lock timeout")
			case <-time.After(5 * time.Second):
				t.Fatal("rename timeout stuck")
			}
			if e.catalog.state.Get(ref.Address) != old {
				t.Fatal("timeout mutated descriptor")
			}
			transactionEnd(t, a, tx, false)
			lockEmpty(t, e)
		})
	}
}
