package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestOperatorClassCatalogAtomicity(t *testing.T) {
	e := New()
	ctx := context.Background()
	base := e.CatalogRead()
	family, class, member := operatorClassUnitDefinitions(t, e, "class_atomic", 351)
	typ, _ := base.LookupType(sqltype.Int4)
	loose := *member
	loose.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	loose.Number = 2
	loose.Function = 3130
	loose.OwningClass = 0
	classDeps := catalog.OperatorFamilyDependencies(base, class)
	wantClass := map[ids.ObjectAddress]byte{class.Namespace(): 'n', {ClassID: ids.TypeClassID, ObjectID: 23}: 'n', family.Address(): 'a'}
	if len(classDeps) != len(wantClass) {
		t.Fatal("class graph degree", classDeps)
	}
	for _, edge := range classDeps {
		if wantClass[edge.Address] != edge.Kind {
			t.Fatal("class graph kind", edge)
		}
	}
	memberDeps := catalog.OperatorFamilyDependencies(base, member)
	if len(memberDeps) != 2 || memberDeps[0].Address != class.Address() || memberDeps[0].Kind != 'i' || memberDeps[1].Address != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 351}) || memberDeps[1].Kind != 'n' {
		t.Fatal("required member graph", memberDeps)
	}
	looseDeps := catalog.OperatorFamilyDependencies(base, &loose)
	if looseDeps[0].Address != family.Address() || looseDeps[0].Kind != 'a' || looseDeps[1].Kind != 'a' {
		t.Fatal("loose member graph", looseDeps)
	}
	setup := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(setup)
	for _, d := range []catalog.Descriptor{family, class, member, &loose} {
		if err := setup.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	classValue, ok := setup.StageRead().IndexClass(class.Address().ObjectID)
	if !ok {
		t.Fatal("private class authority missing")
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: class.Namespace(), ObjectRevision: 1, ObjectName: "class_atomic_table", Owner: class.Owner, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Index: map[string]int{"n": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: classValue, ExplicitClass: true}}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: parent.Namespace(), ObjectRevision: 1, ObjectName: "class_atomic_index", Owner: parent.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition, Attributes: []int32{1}, Columns: parent.Columns, Index: map[string]int{"n": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	for _, d := range []catalog.Descriptor{parent, idx} {
		if err := setup.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	one, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{one}}}}
	setup.initialTables = map[ids.ObjectAddress]*storage.Relation{parent.Address(): store}
	prepared, err := e.ValidateCatalogStage(ctx, setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	root := e.storage.IndexRoots[idx.Address()]
	if root == nil || len(root.Entries) != 1 {
		t.Fatal("custom root not atomically installed")
	}
	unchanged := func() {
		t.Helper()
		if e.catalog.state != current.State() || e.storage.IndexRoots[idx.Address()] != root || e.storage.Relations[parent.Address()] != store {
			t.Fatal("failed candidate changed catalog/data/root")
		}
		if got, ok := e.CatalogRead().OperatorClass(class.Address().ObjectID); !ok || got != class {
			t.Fatal("incumbent class changed")
		}
	}
	// A fully prepared class/family/member batch canceled at publication cannot
	// leak primary/default/name slots or invalidate an incumbent root.
	newFamily, newClass, newMember := operatorClassUnitDefinitions(t, e, "class_atomic_cancel", 181)
	canceled := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(canceled)
	for _, d := range []catalog.Descriptor{newFamily, newClass, newMember} {
		if err := canceled.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	canceledContext, cancel := context.WithCancel(ctx)
	pending, err := e.ValidateCatalogStage(canceledContext, canceled)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalog(pending, PublicationToken{Epoch: current.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal("missing publication cancellation", err)
	}
	unchanged()
	if _, ok := e.CatalogRead().OperatorClass(newClass.Address().ObjectID); ok {
		t.Fatal("canceled class escaped")
	}
	if _, ok := e.CatalogRead().LookupOperatorClass(newClass.Namespace(), 403, newClass.Name()); ok {
		t.Fatal("canceled class name escaped")
	}
	if _, ok := e.CatalogRead().FamilySupport(newFamily.Address().ObjectID, 23, 23, 1); ok {
		t.Fatal("canceled member escaped")
	}
	// A conflicting global default after creating an implicit-family-shaped
	// candidate leaves every incumbent key and object unchanged.
	failed := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(failed)
	if err := failed.Create(newFamily); err != nil {
		t.Fatal(err)
	}
	duplicate := *newClass
	duplicate.Default = true
	if err := failed.Create(&duplicate); err == nil {
		if _, err = e.ValidateCatalogStage(ctx, failed); err == nil {
			t.Fatal("duplicate integer default admitted")
		}
	}
	unchanged()
	incumbent, ok := e.CatalogRead().DefaultOperatorClass(403, 23)
	if !ok || incumbent.Address().ObjectID != 1978 {
		t.Fatal("default slot changed", incumbent)
	}
	// Exercise the common graph, not Candidate.Remove: index NORMAL blocks class
	// RESTRICT, while required member INTERNAL edges retire with class CASCADE.
	restrict := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(restrict)
	err = stageExpressionDependencies(ctx, restrict, []ids.ObjectAddress{class.Address()}, parser.DropRestrict, nil, nil, nil, class.Address())
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "2BP01" {
		t.Fatal("dependent class restrict", err)
	}
	unchanged()
	cascade := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(cascade)
	if err := stageExpressionDependencies(ctx, cascade, []ids.ObjectAddress{class.Address()}, parser.DropCascade, nil, nil, nil, class.Address()); err != nil {
		t.Fatal("class cascade graph", err)
	}
	removed, err := e.ValidateCatalogStage(ctx, cascade)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(removed, PublicationToken{Epoch: current.Epoch()}); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	for _, a := range []ids.ObjectAddress{class.Address(), member.Address(), idx.Address()} {
		if after.Get(a) != nil {
			t.Fatal("class closure survivor", a)
		}
	}
	if after.Get(family.Address()) == nil || after.Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 351}) == nil || after.Get(parent.Address()) == nil {
		t.Fatal("class drop removed family/implementation/table")
	}
	if after.Get(loose.Address()) == nil {
		t.Fatal("class drop removed loose optional member")
	}
	if current.Get(class.Address()) != class || current.Get(member.Address()) != member || len(root.Entries) != 1 {
		t.Fatal("retained generation changed")
	}
}
