package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*Engine, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []ColumnDescriptor
	if !empty {
		columns = []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: testUsage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.mu.Lock()
	source, err := e.newTableDescriptorLocked(ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, "pair", auth.state.CurrentUser, append([]ColumnDescriptor(nil), columns...), index)
	if err == nil {
		source.relationKind = compositeRelation
		if !empty {
			source.attributes = []int32{2, 4}
			source.attributeHighWater = 4
			source.columns[0].OriginAttribute = 2
			source.columns[1].OriginAttribute = 4
		}
		err = e.stageRowTypeLocked(stage, source)
	}
	var table *relationDescriptor
	if err == nil {
		table, err = e.newTableDescriptorLocked(source.namespace, "typed", source.owner, append([]ColumnDescriptor(nil), columns...), index)
	}
	if err == nil {
		table.ofType = ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.rowType)}
		if !empty {
			table.attributes = []int32{1, 7}
			table.attributeHighWater = 7
			table.columns[1].OriginAttribute = 7
		}
		err = e.stageRowTypeLocked(stage, table)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(table); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.state.descriptors[source.address].(*relationDescriptor), view.state.descriptors[table.address].(*relationDescriptor)
}

func TestTypedProviderShapeAndIndependentAttributes(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	if err := validateTypedAttachment(view, table); err != nil {
		t.Fatal(err)
	}
	if table.rowType == source.rowType || !reflect.DeepEqual(table.attributes, []int32{1, 7}) || table.attributeHighWater != 7 {
		t.Fatal("source identity copied into destination", table)
	}
	next, err := renameColumnCandidate(source, "note", "label")
	if err != nil {
		t.Fatal(err)
	}
	actions, renames := typedShapeActions(source, next, table, nil)
	if len(actions) != 0 || !reflect.DeepEqual(renames, map[int32]string{7: "label"}) || source.columns[1].Name != "note" {
		t.Fatal("unstable rename correspondence", actions, renames)
	}
	removed := *source
	removed.columns, removed.attributes, removed.index = source.columns[:1], source.attributes[:1], map[string]int{"id": 0}
	actions, _ = typedShapeActions(source, &removed, table, nil)
	if len(actions) != 1 || actions[0].action.Column.Text != "note" || actions[0].action.Action != parser.DropColumn {
		t.Fatal(actions)
	}
	for _, bad := range []ObjectAddress{{ClassID: RelationClassID, ObjectID: source.address.ObjectID}, {ClassID: TypeClassID, ObjectID: 999999}} {
		invalid := *table
		invalid.ofType = bad
		if err := validateTypedAttachment(view, &invalid); err == nil {
			t.Fatal("invalid attachment admitted", bad)
		}
	}
	invalid := *table
	invalid.ofType = ObjectAddress{ClassID: TypeClassID, ObjectID: OID(table.rowType)}
	if err := validateTypedAttachment(view, &invalid); err == nil {
		t.Fatal("physical own-row source admitted")
	}
	invalid = *table
	invalid.columns = append([]ColumnDescriptor(nil), table.columns...)
	invalid.columns[0].Usage = testUsage(e, sqltype.Int8)
	if err := validateTypedAttachment(view, &invalid); err == nil {
		t.Fatal("different member usage admitted")
	}
}

func TestTypedDirectGuardLookupOrdering(t *testing.T) {
	_, _, table := typedUnitCatalog(t, false)
	for _, tc := range []struct {
		action        parser.AlterTableAction
		code, message string
	}{
		{parser.AlterTableAction{Action: parser.AddColumn, IfNotExists: true}, "42809", "cannot add column to typed table"},
		{parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "absent"}, IfExists: true}, "42809", "cannot drop column from typed table"},
		{parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "absent"}}, "42703", `column "absent" of relation "typed" does not exist`},
		{parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "id"}}, "42809", "cannot alter column type of typed table"},
	} {
		var diagnostic *sqlstate.Error
		err := typedDirectActionsGuard(table, []parser.AlterTableAction{tc.action})
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(err, tc)
		}
	}
	detached := *table
	detached.ofType = ObjectAddress{}
	if err := typedDirectActionsGuard(&detached, []parser.AlterTableAction{{Action: parser.AddColumn}}); err != nil {
		t.Fatal(err)
	}
}

func typedRenameStage(t *testing.T, e *Engine, source, table *relationDescriptor) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.typedSource = table.ofType
	stage.typedObserved = typedClosure(stage.base, table.ofType)
	for _, old := range []*relationDescriptor{source, table} {
		next, err := renameColumnCandidate(old, "note", "label")
		if err != nil {
			t.Fatal(err)
		}
		next.revision++
		if err := stage.Replace(next); err != nil {
			t.Fatal(err)
		}
	}
	return stage
}

func TestTypedPublicationFinalCancellationPreservesRoots(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	before := e.CatalogRead()
	stage := typedRenameStage(t, e, source, table)
	defer e.DiscardCatalogStage(stage)
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || e.CatalogRead().state != before.state {
		t.Fatal("cancelled publication changed catalog", changes, err)
	}
	if source.columns[1].Name != "note" || table.columns[1].Name != "note" {
		t.Fatal("old descriptors mutated")
	}
}

func TestTypedAdmissionLateAttachChildAndDescriptorFence(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, table)
	defer e.DiscardCatalogStage(stage)
	for _, kind := range []string{"attach", "child", "descriptor", "unrelated"} {
		next := &catalogState{epoch: stage.base.Epoch(), descriptors: make(map[ObjectAddress]Descriptor, len(stage.base.state.descriptors)+1)}
		for address, descriptor := range stage.base.state.descriptors {
			next.descriptors[address] = descriptor
		}
		late := *table
		late.address.ObjectID += 1000
		late.name = "late"
		switch kind {
		case "attach":
			next.descriptors[late.address] = &late
		case "child":
			late.ofType = ObjectAddress{}
			late.inheritance = &tableInheritance{parents: []ObjectAddress{table.address}}
			next.descriptors[late.address] = &late
		case "descriptor":
			replacement := *table
			replacement.revision++
			next.descriptors[table.address] = &replacement
		case "unrelated":
			late.ofType = ObjectAddress{}
			next.descriptors[late.address] = &late
		}
		valid := typedObservationValid(stage, CatalogRead{owner: e, state: next})
		if valid != (kind == "unrelated") {
			t.Fatal("incorrect dependency-set fence", kind, valid)
		}
	}
}

func TestTypedEmptyAndBuiltinSourceDumpPredecessor(t *testing.T) {
	for _, empty := range []bool{false, true} {
		e, source, table := typedUnitCatalog(t, empty)
		predecessors, err := dumpColumnPredecessors(e.CatalogRead(), table, nil)
		if err != nil || !reflect.DeepEqual(predecessors, []OID{source.address.ObjectID}) {
			t.Fatal("typed source absent from mixed topology", empty, predecessors, err)
		}
	}
}
