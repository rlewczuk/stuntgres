package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type partialIndexFixture struct {
	e             *core.Engine
	parent, index *catalog.Relation
	predicate     plan.ColumnExpr
	store         *storage.Relation
}

func newPartialIndexFixture(t testing.TB) partialIndexFixture {
	t.Helper()
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20}, ColumnMeta: catalog.ColumnMetadata{HighWater: 20}, Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}, {Name: "active", Usage: boolean.Usage()}}, Index: map[string]int{"k": 0, "active": 1}}
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), parent.Address())
	ref.Address.SubID = 20
	column := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 1, Origin: ref}
	predicate := &catalog.IndexPredicate{Definition: column, Execution: column, Attributes: parent.Attributes, Dependencies: []ids.ObjectRef{ref}}
	class, _ := sqltype.DefaultIndexClass(integer)
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "pi", ObjectRevision: 1, RelationKind: catalog.IndexRelation, Columns: []catalog.Column{parent.Columns[0]}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Method: catalog.BtreeMethod, MethodOID: 403, Ready: true, Valid: true, Live: true, Keys: []catalog.IndexKey{{Attribute: 10, Class: class}}, Predicate: predicate}}
	store := &storage.Relation{}
	for i, k := range []int64{10, 20, 30, 0, 10} {
		value, _ := sqltype.Integer(integer, k)
		if i == 3 {
			value = sqltype.Null(integer)
		}
		active := sqltype.Boolean(boolean, i == 0 || i == 3 || i == 4)
		if i == 2 {
			active = sqltype.Null(boolean)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Values: []sqltype.Value{value, active}})
	}
	e.Catalog.State.SetDescriptor(parent.Address(), parent)
	e.Catalog.State.SetDescriptor(idx.Address(), idx)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(parent.Address(), store)
	e.Mu.Unlock()
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root})
	e.Mu.Unlock()
	return partialIndexFixture{e, parent, idx, column, store}
}

func partialWantMembership(t *testing.T, root *storage.IndexRoot, want map[ids.RowID]ids.VersionID) {
	t.Helper()
	if !reflect.DeepEqual(root.Membership, want) || len(root.Entries) != len(want) {
		t.Fatal("wrong membership", root.Membership, want, root.Entries)
	}
	for _, entry := range root.Entries {
		if root.Membership[entry.Row] != entry.Version {
			t.Fatal("entry/lookup disagreement", entry)
		}
	}
}

func partialMaskFixture(t *testing.T) (partialIndexFixture, *core.StatementScope, exec.Leaf, *visibility.View) {
	f := newPartialIndexFixture(t)
	scope := &core.StatementScope{Owner: &session.TransactionOwner{Isolation: session.ReadCommitted}}
	leaf := exec.Leaf{Ref: f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address()), Relation: f.parent, Storage: f.store, PartialCandidate: f.index}
	data := &visibility.Data{Catalog: f.e.CatalogRead(), Frontier: f.e.Visibility.CommitSeq(), Committed: map[ids.ObjectAddress][]storage.RowVersion{f.parent.Address(): f.store.Versions}, SingleGeneration: f.store}
	view := visibility.NewView(data)
	t.Cleanup(view.Release)
	return f, scope, leaf, view
}
