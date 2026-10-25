package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestTransactionalTypesPrivateRootSelection(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ref := transactionBegin(t, s)
	defer transactionEnd(t, s, ref, false)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 123}
	committed, private := new(storage.IndexRoot), new(storage.IndexRoot)
	e.Storage.IndexRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: committed}
	changes := session.NewJournal()
	index.JournalIndexes(changes).UniqueRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: private}
	s.Transactions.Catalog = changes
	stage := &CatalogStage{AuthSession: s}
	if got := e.domainStageIndexRootLocked(stage, address); got != private {
		t.Fatal("explicit domain stage selected committed rather than private logical root")
	}
	if got := e.domainStageIndexRootLocked(&CatalogStage{}, address); got != committed {
		t.Fatal("unowned stage lost committed root")
	}
	clone := changes.Clone()
	newer := new(storage.IndexRoot)
	index.JournalIndexes(clone).UniqueRoots[address] = newer
	if index.JournalIndexes(changes).UniqueRoots[address] != private {
		t.Fatal("new journal mutated older savepoint index slot")
	}
	s.Transactions.Catalog = clone
	if got := e.domainStageIndexRootLocked(stage, address); got != newer {
		t.Fatal("repeated private evolution ignored current root")
	}
}
