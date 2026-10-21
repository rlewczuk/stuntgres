package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestEnumProvenanceSavepoints(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 45000}
	outer := session.NewJournal()
	outer.Enum = enumSlotOf(&typedef.EnumJournal{Created: map[ids.ObjectAddress]bool{address: true}, Unsafe: map[int64]bool{9: true}})
	child := outer.Clone()
	enumJournalOf(child.Enum).UnsafeMembers()[10] = true
	delete(enumJournalOf(child.Enum).CreatedTypes(), address)
	if enumJournalOf(outer.Enum).UnsafeMembers()[10] || !enumJournalOf(outer.Enum).CreatedTypes()[address] {
		t.Fatal("provenance clone mutated mark")
	}
	owner := &session.TransactionOwner{Catalog: outer}
	mark := session.SavepointMark{Catalog: owner.Catalog}
	owner.Catalog = child
	// RELEASE retains child roots; ROLLBACK restores the exact marked root.
	if typedef.EnumSafety(enumJournalOf(owner.Catalog.Enum).UnsafeMembers())(45000, 10) {
		t.Fatal("child unsafe member upgraded")
	}
	owner.Catalog = mark.Catalog
	if !typedef.EnumSafety(enumJournalOf(owner.Catalog.Enum).UnsafeMembers())(45000, 10) || typedef.EnumSafety(enumJournalOf(owner.Catalog.Enum).UnsafeMembers())(45000, 9) {
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
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE affected(v mood)").(*parser.CreateTable), mood.ObjectNamespace, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE untouched(v integer)").(*parser.CreateTable), mood.ObjectNamespace, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
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
	oldStore := &storage.Relation{Versions: []storage.RowVersion{{Row: 7, Version: 11, Born: 3, KeyChangedAt: 2, Values: []sqltype.Value{value}}}, Generation: 4}
	e.mu.Lock()
	e.storage.InstallRelationLocked(ref.Address, oldStore)
	e.mu.Unlock()
	otherStore := e.storage.Relations[other.Address]
	members := mood.SQLType.EnumMembers()
	members[1].Label = "end"
	members = append(members, sqltype.EnumMember{ID: 100000, Label: "last", SortOrder: 3})
	typ, err := sqltype.EnumWithMembers(mood.SQLType.ID(), mood.Name(), mood.SQLType.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *mood
	next.SQLType = typ
	next.ObjectRevision++
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
	row := shape.replacement.Versions[0]
	oldID, _ := value.EnumMemberID()
	newID, _ := row.Values[0].EnumMemberID()
	if oldID != newID || row.Row != 7 || row.Version != 11 || row.Born != 3 || row.KeyChangedAt != 2 || !reflect.DeepEqual(oldRelation.Attributes, stage.Get(ref.Address).(*catalog.Relation).Attributes) {
		t.Fatal("row/column/member identity rewritten")
	}
	if stage.rowShapes[other.Address].replacement != nil || e.storage.Relations[other.Address] != otherStore {
		t.Fatal("unaffected store copied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); !errors.Is(err, context.Canceled) || e.catalog.state != oldCatalog || e.storage.Relations[ref.Address] != oldStore {
		t.Fatal("canceled partial generation publication", err)
	}
	if text, err := mood.SQLType.Output(context.Background(), oldStore.Versions[0].Values[0]); err != nil || text != "q" {
		t.Fatal("old root mutated", text, err)
	}
}

func TestEnumImportSameIDMembership(t *testing.T) {
	ctx := context.Background()
	source, _ := sqltype.EnumWithMembers(45000, "mood", "public.mood", []sqltype.EnumMember{{ID: 8, Label: "old", SortOrder: 1}})
	target, _ := sqltype.EnumWithMembers(source.ID(), "mood", "public.mood", []sqltype.EnumMember{{ID: 80, Label: "old", SortOrder: 1}})
	v, _ := source.Input(ctx, "old", -1)
	imports := map[*sqltype.Descriptor]*sqltype.Descriptor{source: target}
	if _, err := typedef.ReownDomainDatum(v, target, nil, nil, nil); err == nil {
		t.Fatal("ordinary same-ID foreign membership admitted")
	}
	mapped, err := typedef.ReownDomainDatum(v, target, nil, imports, nil)
	id, _ := mapped.EnumMemberID()
	if err != nil || id != 80 {
		t.Fatal("same-ID import", id, err)
	}
	oldDomain, _ := sqltype.Domain(45001, "d", "public.d", source.Usage())
	newDomain, _ := sqltype.Domain(oldDomain.ID(), "d", "public.d", target.Usage())
	wrapped, _ := oldDomain.Wrap(v)
	mapped, err = typedef.ReownDomainDatum(wrapped, newDomain, nil, imports, nil)
	id, _ = mapped.EnumMemberID()
	if err != nil || id != 80 || mapped.Type() != newDomain {
		t.Fatal("domain import", mapped, err)
	}
	oldArray, _ := sqltype.Array(45002, "_mood", source)
	newArray, _ := sqltype.Array(oldArray.ID(), "_mood", target)
	array, _ := sqltype.OwnedArrayValue(oldArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{v})
	mapped, err = typedef.ReownDomainDatum(array, newArray, nil, imports, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ = mapped.ArrayElement(0).EnumMemberID()
	if id != 80 {
		t.Fatal("nested import member", id)
	}
	importing := catalog.NewState(0)
	importing.SetScalarImports(imports)
	view := New().catalog.read(importing)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: source.Usage()}, Value: v}
	rebound, changed, err := RetagTypes(view, literal, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == source {
			return target
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(err)
	}
	id, _ = rebound.(plan.LiteralExpr).Value.EnumMemberID()
	if id != 80 {
		t.Fatal("stored literal import", id)
	}
}
