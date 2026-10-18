package engine

import (
	"context"
	"testing"
)

func TestAlterSchemaGrantTimeShadow(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SCHEMA shadow")
	sessionSQL(t, b, "SET search_path TO shadow,s1")
	ref := roleTableRef(t, e, "s1.t")
	old, storage := e.catalog.state.Get(ref.Address), e.storage[ref.Address]
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "ALTER TABLE t SET SCHEMA s2", false)
	lockPending(t, e, 1)
	run(t, e, "CREATE TABLE shadow.t(id INT,note TEXT)")
	shadow := roleTableRef(t, e, "shadow.t")
	shadowDescriptor := e.catalog.state.Get(shadow.Address)
	frontier := e.catalog.nextRevision
	transactionEnd(t, a, tx, false)
	lockAwait(t, done, ErrCatalogReplaced)
	if e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != storage || e.catalog.state.Get(shadow.Address) != shadowDescriptor || e.catalog.nextRevision != frontier {
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
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	alterSchemaEngineFixture(t, e)
	replacement := e.catalog.state
	lockAwait(t, done, ErrCatalogReplaced)
	transactionEnd(t, a, tx, false)
	if e.catalog.state != replacement {
		t.Fatal("stale waiter published into replacement")
	}
	lockEmpty(t, e)
}
