package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestOperatorFamilyPublication(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	typ, _ := base.LookupType(sqltype.Int4)
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	builtinFamily, _ := base.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 1976})
	owner := builtinFamily.Owner
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: namespace, ObjectName: "family_publication_table", ObjectRevision: 1, Owner: owner, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Index: map[string]int{"n": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	class, _ := sqltype.DefaultIndexClass(typ)
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: namespace, ObjectName: "family_publication_index", ObjectRevision: 1, Owner: owner, RelationKind: catalog.IndexRelation, SQLIndex: definition, Attributes: []int32{1}, Columns: parent.Columns, Index: map[string]int{"n": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	one, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{one}}}}
	setup := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(setup)
	for _, d := range []catalog.Descriptor{parent, idx} {
		if err := setup.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	SetStageInitialTables(setup, map[ids.ObjectAddress]*storage.Relation{parent.Address(): store})
	preparedSetup, err := e.ValidateCatalogStage(context.Background(), setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, preparedSetup, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	base = e.CatalogRead()
	root := e.Storage.IndexRoots[idx.Address()]
	if root == nil || len(root.Entries) != 1 {
		t.Fatal("setup did not publish the real index root")
	}
	ordering, err := index.BindIndexOrdering(base, parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	support := ordering.Comparisons[0].Support()
	// Ordinary catalog publication installs the descriptor, initial rows and
	// genuine released int4 support root together before the failure trials.
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: namespace, ObjectName: "family_publication", ObjectRevision: 2, Owner: owner, Method: 403}
	member := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 3, Family: family.Address().ObjectID, Left: 23, Right: 23, Number: 1, Function: 351}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Create(family); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(member); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	before := e.Catalog.State
	cancel()
	if _, err = publishCatalog(e, prepared, PublicationToken{Epoch: base.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal("publication cancellation", err)
	}
	if e.Catalog.State != before || e.Storage.IndexRoots[idx.Address()] != root || e.Storage.Relations[parent.Address()] != store {
		t.Fatal("cancellation published metadata/root")
	}
	if got, ok := e.CatalogRead().FamilySupport(member.Family, 23, 23, 1); ok || got != nil {
		t.Fatal("canceled member escaped")
	}
	success := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(success)
	if err := success.Create(family); err != nil {
		t.Fatal(err)
	}
	if err := success.Create(member); err != nil {
		t.Fatal(err)
	}
	publication, err := e.ValidateCatalogStage(context.Background(), success)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	if got, ok := current.FamilySupport(member.Family, 23, 23, 1); !ok || got != member {
		t.Fatal("published member not selected", got)
	}
	if _, ok := base.FamilySupport(member.Family, 23, 23, 1); ok {
		t.Fatal("retained source catalog changed")
	}
	if e.Storage.IndexRoots[idx.Address()] != root || e.Storage.Relations[parent.Address()] != store || ordering.Comparisons[0].Support() != support {
		t.Fatal("loose custom family changed unrelated builtin root/provider")
	}
	invalid := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(invalid)
	bad := *member
	bad.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	bad.Number = 7
	if err := invalid.Create(&bad); err != nil {
		t.Fatal(err)
	}
	before = e.Catalog.State
	if _, err := e.ValidateCatalogStage(context.Background(), invalid); err == nil {
		t.Fatal("invalid support ceiling published")
	}
	if e.Catalog.State != before || e.Storage.IndexRoots[idx.Address()] != root {
		t.Fatal("validation failure changed publication")
	}
	// Exercise the ordinary dependency/removal path, not a direct Candidate
	// deletion: DROP targets must actually become published removals.
	drop := func(address ids.ObjectAddress) {
		t.Helper()
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		if err := StageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{address}, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
			t.Fatal("drop dependencies", err)
		}
		publication, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal("drop validation", err)
		}
		if _, err := publishCatalog(e, publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal("drop publication", err)
		}
	}
	drop(member.Address())
	if got, ok := e.CatalogRead().FamilySupport(member.Family, 23, 23, 1); ok || got != nil || e.CatalogRead().Get(member.Address()) != nil {
		t.Fatal("support DROP retained descriptor or primary key")
	}
	if got, ok := current.FamilySupport(member.Family, 23, 23, 1); !ok || got != member {
		t.Fatal("support DROP mutated retained catalog")
	}
	readded := *member
	readded.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	readded.ObjectRevision++
	operator := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 4, Family: member.Family, Left: 23, Right: 23, Number: 1, Purpose: 's', Operator: 97, Method: 403}
	add := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(add)
	for _, d := range []catalog.Descriptor{&readded, operator} {
		if err := add.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	republication, err := e.ValidateCatalogStage(context.Background(), add)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, republication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if got, ok := e.CatalogRead().FamilySupport(member.Family, 23, 23, 1); !ok || got != &readded || got.Address() == member.Address() {
		t.Fatal("reADD did not select fresh support identity")
	}
	drop(operator.Address())
	if got, ok := e.CatalogRead().FamilyOperator(member.Family, 23, 23, 1, 's'); ok || got != nil || e.CatalogRead().Get(operator.Address()) != nil {
		t.Fatal("operator DROP retained descriptor or primary key")
	}
	// The secondary key must retire with the member, so the same operator
	// can be added again under a fresh identity before family AUTO closure.
	readdedOperator := *operator
	readdedOperator.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	addOperator := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(addOperator)
	if err := addOperator.Create(&readdedOperator); err != nil {
		t.Fatal("operator secondary key survived DROP", err)
	}
	operatorPublication, err := e.ValidateCatalogStage(context.Background(), addOperator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, operatorPublication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	drop(family.Address())
	for _, d := range []catalog.Descriptor{family, &readded, &readdedOperator} {
		if e.CatalogRead().Get(d.Address()) != nil || retained.Get(d.Address()) != d {
			t.Fatal("family DROP failed AUTO closure or mutated retained catalog", d.Address())
		}
	}
	if e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 97}) == nil {
		t.Fatal("family DROP removed underlying operator")
	}
	if e.Storage.IndexRoots[idx.Address()] != root || e.Storage.Relations[parent.Address()] != store || ordering.Comparisons[0].Support() != support {
		t.Fatal("member/family removal changed unrelated builtin root/provider")
	}
}
