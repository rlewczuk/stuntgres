package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestAlterSchemaGrantTimeShadow(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SCHEMA shadow")
	enginetest.SessionSQL(t, b, "SET search_path TO shadow,s1")
	ref := enginetest.RoleTableRef(t, e, "s1.t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := enginetest.LockAsync(e, b, context.Background(), "ALTER TABLE t SET SCHEMA s2", false)
	enginetest.LockPending(t, e, 1)
	enginetest.Run(t, e, "CREATE TABLE shadow.t(id INT,note TEXT)")
	shadow := enginetest.RoleTableRef(t, e, "shadow.t")
	shadowDescriptor := e.Catalog.State.Get(shadow.Address)
	frontier := e.Catalog.NextRevision()
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.State.Get(shadow.Address) != shadowDescriptor || e.Catalog.NextRevision() != frontier {
		t.Fatal("move retargeted grant-time shadow")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterSchemaGrantTimeReplacement(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := enginetest.LockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
	enginetest.LockPending(t, e, 1)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	alterSchemaEngineFixture(t, e)
	replacement := e.Catalog.State
	enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
	enginetest.TransactionEnd(t, a, tx, false)
	if e.Catalog.State != replacement {
		t.Fatal("stale waiter published into replacement")
	}
	enginetest.LockEmpty(t, e)
}
