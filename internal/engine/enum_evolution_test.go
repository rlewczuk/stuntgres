package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestEnumProvenanceSavepoints(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 45000}
	outer := newCatalogChanges()
	outer.enumCreated = map[ids.ObjectAddress]bool{address: true}
	outer.enumUnsafe = map[int64]bool{9: true}
	child := cloneCatalogChanges(outer)
	child.enumUnsafe[10] = true
	delete(child.enumCreated, address)
	if outer.enumUnsafe[10] || !outer.enumCreated[address] {
		t.Fatal("provenance clone mutated mark")
	}
	owner := &transactionOwner{catalog: outer}
	mark := savepointMark{catalog: owner.catalog}
	owner.catalog = child
	// RELEASE retains child roots; ROLLBACK restores the exact marked root.
	if enumSafety(owner.catalog.enumUnsafe)(45000, 10) {
		t.Fatal("child unsafe member upgraded")
	}
	owner.catalog = mark.catalog
	if !enumSafety(owner.catalog.enumUnsafe)(45000, 10) || enumSafety(owner.catalog.enumUnsafe)(45000, 9) {
		t.Fatal("rollback provenance")
	}
}

func TestEnumGenerationPublicationAtomicity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('z','q')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	stage := e.BeginCatalogStage(e.CatalogRead())
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE affected(v mood)").(*parser.CreateTable), mood.namespace, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE untouched(v integer)").(*parser.CreateTable), mood.namespace, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	ref := catalogRef(t, e, e.CatalogRead(), "affected")
	other := catalogRef(t, e, e.CatalogRead(), "untouched")
	oldCatalog := e.catalog.state
	oldRelation := oldCatalog.MustRelation(ref.Address)
	value, _ := mood.SQLType.Input(context.Background(), "q", -1)
	oldStore := &relationStorage{versions: []rowVersion{{row: 7, version: 11, born: 3, keyChangedAt: 2, values: []sqltype.Value{value}}}, generation: 4}
	e.storage[ref.Address] = oldStore
	otherStore := e.storage[other.Address]
	members := mood.SQLType.EnumMembers()
	members[1].Label = "end"
	members = append(members, sqltype.EnumMember{ID: 100000, Label: "last", SortOrder: 3})
	typ, err := sqltype.EnumWithMembers(mood.SQLType.ID(), mood.Name(), mood.SQLType.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *mood
	next.SQLType = typ
	next.revision++
	stage = e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.reownStagedTypeGenerationsLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	shape := stage.rowShapes[ref.Address]
	row := shape.replacement.versions[0]
	oldID, _ := value.EnumMemberID()
	newID, _ := row.values[0].EnumMemberID()
	if oldID != newID || row.row != 7 || row.version != 11 || row.born != 3 || row.keyChangedAt != 2 || !reflect.DeepEqual(oldRelation.Attributes, stage.descriptors[ref.Address].(*relationDescriptor).Attributes) {
		t.Fatal("row/column/member identity rewritten")
	}
	if stage.rowShapes[other.Address].replacement != nil || e.storage[other.Address] != otherStore {
		t.Fatal("unaffected store copied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); !errors.Is(err, context.Canceled) || e.catalog.state != oldCatalog || e.storage[ref.Address] != oldStore {
		t.Fatal("canceled partial generation publication", err)
	}
	if text, err := mood.SQLType.Output(context.Background(), oldStore.versions[0].values[0]); err != nil || text != "q" {
		t.Fatal("old root mutated", text, err)
	}
}

func TestEnumImportSameIDMembership(t *testing.T) {
	ctx := context.Background()
	source, _ := sqltype.EnumWithMembers(45000, "mood", "public.mood", []sqltype.EnumMember{{ID: 8, Label: "old", SortOrder: 1}})
	target, _ := sqltype.EnumWithMembers(source.ID(), "mood", "public.mood", []sqltype.EnumMember{{ID: 80, Label: "old", SortOrder: 1}})
	v, _ := source.Input(ctx, "old", -1)
	imports := map[*sqltype.Descriptor]*sqltype.Descriptor{source: target}
	if _, err := reownDomainDatum(v, target, nil, nil, nil); err == nil {
		t.Fatal("ordinary same-ID foreign membership admitted")
	}
	mapped, err := reownDomainDatum(v, target, nil, imports, nil)
	id, _ := mapped.EnumMemberID()
	if err != nil || id != 80 {
		t.Fatal("same-ID import", id, err)
	}
	oldDomain, _ := sqltype.Domain(45001, "d", "public.d", source.Usage())
	newDomain, _ := sqltype.Domain(oldDomain.ID(), "d", "public.d", target.Usage())
	wrapped, _ := oldDomain.Wrap(v)
	mapped, err = reownDomainDatum(wrapped, newDomain, nil, imports, nil)
	id, _ = mapped.EnumMemberID()
	if err != nil || id != 80 || mapped.Type() != newDomain {
		t.Fatal("domain import", mapped, err)
	}
	oldArray, _ := sqltype.Array(45002, "_mood", source)
	newArray, _ := sqltype.Array(oldArray.ID(), "_mood", target)
	array, _ := sqltype.OwnedArrayValue(oldArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{v})
	mapped, err = reownDomainDatum(array, newArray, nil, imports, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ = mapped.ArrayElement(0).EnumMemberID()
	if id != 80 {
		t.Fatal("nested import member", id)
	}
	view := New().catalog.read(&catalogState{scalarImports: imports})
	literal := literalExpression{Info: expr.Info{Usage: source.Usage()}, value: v}
	rebound, changed, err := retagBoundTypes(view, literal, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == source {
			return target
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(err)
	}
	id, _ = rebound.(literalExpression).value.EnumMemberID()
	if id != 80 {
		t.Fatal("stored literal import", id)
	}
}
