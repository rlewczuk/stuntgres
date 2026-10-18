package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestUpdateCoreSameLengthCatalogGuard(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	old := e.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	next := *old
	next.revision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	original := e.storage[ref.Address]
	if err := stage.alterRelation(context.Background(), ref, &next, original, original, len(original.versions), original.generation, original.ends); err != nil {
		t.Fatal(err)
	}
	generation := original.generation
	sessionSQL(t, a, "UPDATE t SET v=2")
	if original.generation <= generation || len(original.versions) != 1 {
		t.Fatal("same-length update not versioned")
	}
	_, err := e.ValidateCatalogStage(context.Background(), stage)
	var conflict CatalogConflict
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
	beforeT := append([]rowVersion(nil), e.storage[tRef.Address].versions...)
	beforeU := append([]rowVersion(nil), e.storage[uRef.Address].versions...)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=7")
	sessionSQL(t, a, "UPDATE u SET v=8")
	// Corrupt only test-owned private provenance to exercise final all-relations
	// rejection independent of manager prevention of ordinary stale predecessors.
	a.transactions.writes[uRef.Address][0].base++
	op := transactionOperation(t, a)
	_, err := e.Commit(op, tx)
	if err == nil {
		t.Fatal("stale predecessor published")
	}
	if !reflect.DeepEqual(beforeT, e.storage[tRef.Address].versions) || !reflect.DeepEqual(beforeU, e.storage[uRef.Address].versions) {
		t.Fatal("partial multi-relation commit")
	}
	transactionRows(t, b, "SELECT v FROM t", 1)
	transactionRows(t, b, "SELECT v FROM u", 2)
	if a.TransactionState() != TxIdle {
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
	root := e.storage[ref.Address].versions
	before := root[0]
	if _, err := e.ReplaceCatalog(context.Background(), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	current := e.storage[ref.Address].versions[0]
	if !reflect.DeepEqual(before, root[0]) || current.row == before.row || current.version <= before.version || current.born <= before.born {
		t.Fatal("import identities/retention", before, root[0], current)
	}
	transactionRows(t, a, "SELECT v FROM t", 2)
}
