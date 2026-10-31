package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func concurrentUnitIndex(t *testing.T, e *core.Engine, name string, ready, valid bool) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	index, ok := e.Catalog.State.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Ready != ready || index.SQLIndex.Valid != valid || !index.SQLIndex.Live {
		t.Fatalf("index %s has unexpected phase: %#v", name, index)
	}
	return index, e.Storage.IndexRoots[address]
}

func concurrentUnitRootMatchesStorage(t *testing.T, e *core.Engine, idx *catalog.Relation) {
	t.Helper()
	e.Mu.Lock()
	parent := e.Catalog.State.MustRelation(idx.SQLIndex.Parent)
	snapshot := *e.Storage.Relations[parent.Address()]
	root := e.Storage.IndexRoots[idx.Address()]
	e.Mu.Unlock()
	expected, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, &snapshot, nil)
	if err != nil || root == nil || expected == nil {
		t.Fatalf("could not compare installed logical root with live row-version build: %v", err)
	}
	if !reflect.DeepEqual(root, expected) {
		t.Fatalf("installed logical root differs from actual live row-version build: %v", err)
	}
}
