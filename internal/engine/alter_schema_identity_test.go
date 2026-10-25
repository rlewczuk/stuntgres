package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterSchemaGrantTimeShadow(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SCHEMA shadow")
	sessionSQL(t, b, "SET search_path TO shadow,s1")
	ref := roleTableRef(t, e, "s1.t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "ALTER TABLE t SET SCHEMA s2", false)
	lockPending(t, e, 1)
	run(t, e, "CREATE TABLE shadow.t(id INT,note TEXT)")
	shadow := roleTableRef(t, e, "shadow.t")
	shadowDescriptor := e.Catalog.State.Get(shadow.Address)
	frontier := e.Catalog.NextRevision()
	transactionEnd(t, a, tx, false)
	lockAwait(t, done, ids.ErrCatalogReplaced)
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.State.Get(shadow.Address) != shadowDescriptor || e.Catalog.NextRevision() != frontier {
		t.Fatal("move retargeted grant-time shadow")
	}
	lockEmpty(t, e)
}

func TestAlterSchemaGrantTimeReplacement(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
	lockPending(t, e, 1)
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	alterSchemaEngineFixture(t, e)
	replacement := e.Catalog.State
	lockAwait(t, done, ids.ErrCatalogReplaced)
	transactionEnd(t, a, tx, false)
	if e.Catalog.State != replacement {
		t.Fatal("stale waiter published into replacement")
	}
	lockEmpty(t, e)
}
