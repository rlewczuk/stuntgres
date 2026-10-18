package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCompositeAlterFunctionalRetainedFields(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	original, _ := e.CatalogRead().LookupType(source.RowType)
	key := preparedCompositeKey{kind: compositeSelection}
	capture := compositeOccurrence{kind: compositeSelection, base: original.Usage(), attributes: []int32{source.Attributes[1]}}
	for _, members := range [][]sqltype.CompositeMember{{original.CompositeMember(0)}, {}, {{Attribute: 9, Name: "note", Usage: testUsage(e, sqltype.Text)}}} {
		current, err := sqltype.Composite(original.ID(), original.Name(), original.SQLName(), original.AssociatedArray(), members)
		if err != nil {
			t.Fatal(err)
		}
		binder := expressionBinder{engine: e}
		base := literalExpression{expr.Info{Usage: current.Usage()}, sqltype.Null(current)}
		node := binder.retainedCompositeField(base, key, capture, parser.Position{})
		literal, ok := node.(literalExpression)
		if !ok || !literal.value.IsNull() || node.ResultUsage() != original.CompositeMember(1).Usage {
			t.Fatal("retired field retargeted", node)
		}
		capture = binder.composites[key]
		if capture.base.Type != original || capture.attributes[0] != source.Attributes[1] {
			t.Fatal("original capture overwritten")
		}
	}
	binder := expressionBinder{engine: e}
	base := literalExpression{expr.Info{Usage: original.Usage()}, sqltype.Null(original)}
	if field, ok := binder.retainedCompositeField(base, key, capture, parser.Position{}).(fieldExpression); !ok || field.attribute != source.Attributes[1] {
		t.Fatal("live identity lost")
	}
}

func TestCompositeAlterMaterializedStoredGuard(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	before := e.CatalogRead()
	row, _ := before.LookupType(source.RowType)
	array, _ := before.LookupArray(row)
	domain, err := sqltype.Domain(60000, "wrapped", "public.wrapped", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{row, array, domain} {
		consumer := *table
		consumer.OfType = ids.ObjectAddress{}
		consumer.RelationKind = materializedViewRelation
		consumer.Columns = []ColumnDescriptor{{Name: "v", Usage: typ.Usage()}}
		state := *before.State()
		state.CloneDescriptors()
		state.SetDescriptor(table.Address(), &consumer)
		view := e.catalog.read(&state)
		domainUnitDiagnostic(t, rowRewriteError(view, source), "0A000", `cannot alter type "pair" because column "typed.v" uses it`)
		stage := e.BeginCatalogStage(before)
		stage.typedSource = table.OfType
		stage.typedObserved = typedClosure(before, table.OfType)
		if typedObservationValid(stage, view) {
			t.Fatal("late materialized consumer escaped final observation")
		}
		e.DiscardCatalogStage(stage)
		consumer.RelationKind = viewRelation
		if err := rowRewriteError(view, source); err != nil {
			t.Fatal("nonstored view blocked", err)
		}
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("guard published a candidate")
	}
}

func TestCompositeAlterDomainCheckPruning(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN checked_pair AS pair CONSTRAINT field_check CHECK ((VALUE).id>0) CONSTRAINT keep_check CHECK ((VALUE).note<>'bad')"))
	before := e.CatalogRead()
	original := domainUnitType(t, before, "checked_pair")
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	err := stageCompositeFieldChecks(stage, source, source.Attributes[0], parser.DropRestrict, nil)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop column id of composite type pair because other objects depend on it")
	if len(stage.descriptors) != 0 {
		t.Fatal("RESTRICT staged removal")
	}
	if err := stageCompositeFieldChecks(stage, source, source.Attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	next := stage.descriptors[original.Address()].(*typeDescriptor)
	if len(next.DomainChecks) != 1 || next.DomainChecks[0].Name != "keep_check" || next.DomainChecks[0].OID != original.DomainChecks[1].OID || next.SQLType.ID() != original.SQLType.ID() || next.SQLType.DomainBase() != original.SQLType.DomainBase() {
		t.Fatal("wrong owner or constraint pruned")
	}
	if len(original.DomainChecks) != 2 || next.SQLType == original.SQLType {
		t.Fatal("original domain authority mutated")
	}
	if !next.SQLType.HasDomainChecks() {
		t.Fatal("surviving CHECK lost codec authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation failed", err)
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("private pruning published")
	}
	if err := stage.removeDomainCheck(next, next.DomainChecks[0].OID); err != nil {
		t.Fatal(err)
	}
	if len(stage.descriptors[original.Address()].(*typeDescriptor).DomainChecks) != 0 || len(next.DomainChecks) != 1 {
		t.Fatal("last removal mutated surviving capture")
	}
	if stage.descriptors[original.Address()].(*typeDescriptor).SQLType.HasDomainChecks() {
		t.Fatal("empty CHECK owner retained codec authority")
	}
}

func TestCompositeAlterInheritedCheckStageAuthority(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	left := domainUnitTable(t, e, op, "CREATE TABLE left_child(v pair, CONSTRAINT field_check CHECK ((v).id>0), CONSTRAINT keep_check CHECK ((v).note<>'bad')) INHERITS(typed)")
	right := domainUnitTable(t, e, op, "CREATE TABLE right_child() INHERITS(left_child)")
	child := domainUnitTable(t, e, op, "CREATE TABLE child() INHERITS(left_child,right_child)")
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	stage.typedSource = table.OfType
	stage.typedObserved = typedClosure(before, table.OfType)
	if err := stageCompositeFieldChecks(stage, source, source.Attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	for _, original := range []*relationDescriptor{left, right, child} {
		pruned := stage.descriptors[original.Address()].(*relationDescriptor)
		if len(pruned.CheckConstraints) != 1 || pruned.CheckConstraints[0].Name != "keep_check" || len(original.CheckConstraints) != 2 {
			t.Fatal("diamond owner pruning lost", original.Name())
		}
		if _, exists := pruned.Inheritance.Checks["field_check"]; exists {
			t.Fatal("diamond retained retired origin", original.Name())
		}
	}
	view := e.stageRead(stage)
	current := view.MustRelation(child.Address())
	if len(current.CheckConstraints) != 1 || current.CheckConstraints[0].Name != "keep_check" || len(child.CheckConstraints) != 2 {
		t.Fatal("pruning authority lost")
	}
	actions, renames, graph, err := typedInheritedActions(view, current, nil)
	if err != nil || len(actions) != 0 || len(renames) != 0 {
		t.Fatal("unrelated inherited shape changed", err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := e.typedMutation(&StatementScope{op: op}, op, auth, stage, current, actions)
	if err != nil {
		t.Fatal(err)
	}
	next := *mutation.next
	finishTypedInheritance(view, &next, graph)
	if len(next.CheckConstraints) != 1 || next.CheckConstraints[0].Name != "keep_check" {
		t.Fatal("propagation resurrected removed check")
	}
	if _, exists := next.Inheritance.Checks["field_check"]; exists {
		t.Fatal("retired constraint origin retained")
	}
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal("pruned inheritance candidate escaped cancellation", err)
	}
	if len(child.CheckConstraints) != 2 || e.CatalogRead().State() != before.State() {
		t.Fatal("staged pruning mutated published roots")
	}
}
