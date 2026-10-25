package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/control"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []catalog.Column
	if !empty {
		columns = []catalog.Column{{Name: "id", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: testUsage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.Mu.Lock()
	source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "pair", auth.State.CurrentUser, append([]catalog.Column(nil), columns...), index)
	if err == nil {
		source.RelationKind = catalog.CompositeRelation
		if !empty {
			source.Attributes = []int32{2, 4}
			source.ColumnMeta.HighWater = 4
			source.Columns[0].OriginAttribute = 2
			source.Columns[1].OriginAttribute = 4
		}
		err = e.StageRowTypeLocked(stage, source)
	}
	var tbl *catalog.Relation
	if err == nil {
		tbl, err = table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "typed", source.Owner, append([]catalog.Column(nil), columns...), index)
	}
	if err == nil {
		tbl.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
		if !empty {
			tbl.Attributes = []int32{1, 7}
			tbl.ColumnMeta.HighWater = 7
			tbl.Columns[1].OriginAttribute = 7
		}
		err = e.StageRowTypeLocked(stage, tbl)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(tbl); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.MustRelation(source.Address()), view.MustRelation(tbl.Address())
}

func TestTypedProviderShapeAndIndependentAttributes(t *testing.T) {
	e, source, tbl := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	if err := typedef.ValidateTypedAttachment(view, tbl); err != nil {
		t.Fatal(err)
	}
	if tbl.RowType == source.RowType || !reflect.DeepEqual(tbl.Attributes, []int32{1, 7}) || tbl.ColumnMeta.HighWater != 7 {
		t.Fatal("source identity copied into destination", tbl)
	}
	next, err := table.RenameColumnCandidate(source, "note", "label")
	if err != nil {
		t.Fatal(err)
	}
	actions, renames := typedef.TypedShapeActions(source, next, tbl, nil)
	if len(actions) != 0 || !reflect.DeepEqual(renames, map[int32]string{7: "label"}) || source.Columns[1].Name != "note" {
		t.Fatal("unstable rename correspondence", actions, renames)
	}
	removed := *source
	removed.Columns, removed.Attributes, removed.Index = source.Columns[:1], source.Attributes[:1], map[string]int{"id": 0}
	actions, _ = typedef.TypedShapeActions(source, &removed, tbl, nil)
	if len(actions) != 1 || actions[0].Action.Column.Text != "note" || actions[0].Action.Action != parser.DropColumn {
		t.Fatal(actions)
	}
	for _, bad := range []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: source.Address().ObjectID}, {ClassID: ids.TypeClassID, ObjectID: 999999}} {
		invalid := *tbl
		invalid.OfType = bad
		if err := typedef.ValidateTypedAttachment(view, &invalid); err == nil {
			t.Fatal("invalid attachment admitted", bad)
		}
	}
	invalid := *tbl
	invalid.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(tbl.RowType)}
	if err := typedef.ValidateTypedAttachment(view, &invalid); err == nil {
		t.Fatal("physical own-row source admitted")
	}
	invalid = *tbl
	invalid.Columns = append([]catalog.Column(nil), tbl.Columns...)
	invalid.Columns[0].Usage = testUsage(e, sqltype.Int8)
	if err := typedef.ValidateTypedAttachment(view, &invalid); err == nil {
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
		err := typedef.TypedDirectActionsGuard(table, []parser.AlterTableAction{tc.action})
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(err, tc)
		}
	}
	detached := *table
	detached.OfType = ids.ObjectAddress{}
	if err := typedef.TypedDirectActionsGuard(&detached, []parser.AlterTableAction{{Action: parser.AddColumn}}); err != nil {
		t.Fatal(err)
	}
}

func typedRenameStage(t *testing.T, e *core.Engine, source, tbl *catalog.Relation) *core.CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	typedStage(stage).Source = tbl.OfType
	typedStage(stage).Observed = table.TypedClosure(stage.Base, tbl.OfType)
	for _, old := range []*catalog.Relation{source, tbl} {
		next, err := table.RenameColumnCandidate(old, "note", "label")
		if err != nil {
			t.Fatal(err)
		}
		next.ObjectRevision++
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
	changes, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() {
		t.Fatal("cancelled publication changed catalog", changes, err)
	}
	if source.Columns[1].Name != "note" || table.Columns[1].Name != "note" {
		t.Fatal("old descriptors mutated")
	}
}

func TestTypedAdmissionLateAttachChildAndDescriptorFence(t *testing.T) {
	e, source, tbl := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, tbl)
	defer e.DiscardCatalogStage(stage)
	for _, kind := range []string{"attach", "child", "descriptor", "unrelated"} {
		next := catalog.NewState(stage.Base.Epoch())
		for address, descriptor := range stage.Base.Descriptors() {
			next.SetDescriptor(address, descriptor)
		}
		late := *tbl
		late.ObjectAddress.ObjectID += 1000
		late.ObjectName = "late"
		switch kind {
		case "attach":
			next.SetDescriptor(late.Address(), &late)
		case "child":
			late.OfType = ids.ObjectAddress{}
			late.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(tbl.Address())}
			next.SetDescriptor(late.Address(), &late)
		case "descriptor":
			replacement := *tbl
			replacement.ObjectRevision++
			next.SetDescriptor(tbl.Address(), &replacement)
		case "unrelated":
			late.OfType = ids.ObjectAddress{}
			next.SetDescriptor(late.Address(), &late)
		}
		valid := table.TypedObservationValid(stage.Typed, e.Catalog.Read(next))
		if valid != (kind == "unrelated") {
			t.Fatal("incorrect dependency-set fence", kind, valid)
		}
	}
}

func TestTypedEmptyAndBuiltinSourceDumpPredecessor(t *testing.T) {
	for _, empty := range []bool{false, true} {
		e, source, table := typedUnitCatalog(t, empty)
		predecessors, err := control.ColumnPredecessors(e.CatalogRead(), table, nil)
		if err != nil || !reflect.DeepEqual(predecessors, []ids.OID{source.Address().ObjectID}) {
			t.Fatal("typed source absent from mixed topology", empty, predecessors, err)
		}
	}
}
