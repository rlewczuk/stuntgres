package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"testing"
)

func TestUpdateCoreSameLengthCatalogGuard(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
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
	sessionSQL(t, a, "UPDATE t SET v=2")
	if original.Generation <= generation || len(original.Versions) != 1 {
		t.Fatal("same-length update not versioned")
	}
	_, err := e.ValidateCatalogStage(context.Background(), stage)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("stale shape accepted", err)
	}
	transactionRows(t, a, "SELECT v FROM t", 2)
}

func TestUpdateCoreMultiRelationPreflight(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "CREATE TABLE u(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	sessionSQL(t, b, "INSERT INTO u VALUES(2)")
	tRef := catalogRef(t, e, e.CatalogRead(), "t")
	uRef := catalogRef(t, e, e.CatalogRead(), "u")
	beforeT := append([]storage.RowVersion(nil), e.Storage.Relations[tRef.Address].Versions...)
	beforeU := append([]storage.RowVersion(nil), e.Storage.Relations[uRef.Address].Versions...)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=7")
	sessionSQL(t, a, "UPDATE u SET v=8")
	// Corrupt only test-owned private provenance to exercise final all-relations
	// rejection independent of manager prevention of ordinary stale predecessors.
	a.Transactions.Writes[uRef.Address][0].Base++
	op := transactionOperation(t, a)
	_, err := e.Commit(op, tx)
	if err == nil {
		t.Fatal("stale predecessor published")
	}
	if !reflect.DeepEqual(beforeT, e.Storage.Relations[tRef.Address].Versions) || !reflect.DeepEqual(beforeU, e.Storage.Relations[uRef.Address].Versions) {
		t.Fatal("partial multi-relation commit")
	}
	transactionRows(t, b, "SELECT v FROM t", 1)
	transactionRows(t, b, "SELECT v FROM u", 2)
	if a.TransactionState() != session.TxIdle {
		transactionEnd(t, a, tx, false)
	}
	lockEmpty(t, e)
}

func TestUpdateCoreSelfImportRemintsWithoutMutatingViews(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "UPDATE t SET v=2")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	root := e.Storage.Relations[ref.Address].Versions
	before := root[0]
	if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	current := e.Storage.Relations[ref.Address].Versions[0]
	if !reflect.DeepEqual(before, root[0]) || current.Row == before.Row || current.Version <= before.Version || current.Born <= before.Born {
		t.Fatal("import identities/retention", before, root[0], current)
	}
	transactionRows(t, a, "SELECT v FROM t", 2)
}
