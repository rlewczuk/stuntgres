package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestUpdateCoreSameLengthCatalogGuard(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	original := e.Storage.Relations[ref.Address]
	if err := stage.AlterRelation(context.Background(), ref, &next, original, original, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	generation := original.Generation
	enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
	if original.Generation <= generation || len(original.Versions) != 1 {
		t.Fatal("same-length update not versioned")
	}
	_, err := e.ValidateCatalogStage(context.Background(), stage)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("stale shape accepted", err)
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 2)
}

func TestUpdateCoreMultiRelationPreflight(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "CREATE TABLE u(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(2)")
	tRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	uRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "u")
	beforeT := append([]storage.RowVersion(nil), e.Storage.Relations[tRef.Address].Versions...)
	beforeU := append([]storage.RowVersion(nil), e.Storage.Relations[uRef.Address].Versions...)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=7")
	enginetest.SessionSQL(t, a, "UPDATE u SET v=8")
	// Corrupt only test-owned private provenance to exercise final all-relations
	// rejection independent of manager prevention of ordinary stale predecessors.
	a.Transactions.Writes[uRef.Address][0].Base++
	op := enginetest.TransactionOperation(t, a)
	_, err := e.Commit(op, tx)
	if err == nil {
		t.Fatal("stale predecessor published")
	}
	if !reflect.DeepEqual(beforeT, e.Storage.Relations[tRef.Address].Versions) || !reflect.DeepEqual(beforeU, e.Storage.Relations[uRef.Address].Versions) {
		t.Fatal("partial multi-relation commit")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
	enginetest.TransactionRows(t, b, "SELECT v FROM u", 2)
	if a.TransactionState() != session.TxIdle {
		enginetest.TransactionEnd(t, a, tx, false)
	}
	enginetest.LockEmpty(t, e)
}

func TestUpdateCoreSelfImportRemintsWithoutMutatingViews(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	root := e.Storage.Relations[ref.Address].Versions
	before := root[0]
	if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	current := e.Storage.Relations[ref.Address].Versions[0]
	if !reflect.DeepEqual(before, root[0]) || current.Row == before.Row || current.Version <= before.Version || current.Born <= before.Born {
		t.Fatal("import identities/retention", before, root[0], current)
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 2)
}
