package engine

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCompositeAlterFunctionalRetainedFields(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	original, _ := e.CatalogRead().LookupType(source.rowType)
	key := preparedCompositeKey{kind: compositeSelection}
	capture := compositeOccurrence{kind: compositeSelection, base: original.Usage(), attributes: []int32{source.attributes[1]}}
	for _, members := range [][]sqltype.CompositeMember{{original.CompositeMember(0)}, {}, {{Attribute: 9, Name: "note", Usage: testUsage(e, sqltype.Text)}}} {
		current, err := sqltype.Composite(original.ID(), original.Name(), original.SQLName(), original.AssociatedArray(), members)
		if err != nil {
			t.Fatal(err)
		}
		binder := expressionBinder{engine: e}
		base := literalExpression{expressionInfo{Usage: current.Usage()}, sqltype.Null(current)}
		node := binder.retainedCompositeField(base, key, capture, parser.Position{})
		literal, ok := node.(literalExpression)
		if !ok || !literal.value.IsNull() || node.ResultUsage() != original.CompositeMember(1).Usage {
			t.Fatal("retired field retargeted", node)
		}
		capture = binder.composites[key]
		if capture.base.Type != original || capture.attributes[0] != source.attributes[1] {
			t.Fatal("original capture overwritten")
		}
	}
	binder := expressionBinder{engine: e}
	base := literalExpression{expressionInfo{Usage: original.Usage()}, sqltype.Null(original)}
	if field, ok := binder.retainedCompositeField(base, key, capture, parser.Position{}).(fieldExpression); !ok || field.attribute != source.attributes[1] {
		t.Fatal("live identity lost")
	}
}

func TestCompositeAlterMaterializedStoredGuard(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	before := e.CatalogRead()
	row, _ := before.LookupType(source.rowType)
	array, _ := before.LookupArray(row)
	domain, err := sqltype.Domain(60000, "wrapped", "public.wrapped", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{row, array, domain} {
		consumer := *table
		consumer.ofType = ids.ObjectAddress{}
		consumer.relationKind = materializedViewRelation
		consumer.columns = []ColumnDescriptor{{Name: "v", Usage: typ.Usage()}}
		state := *before.state
		state.descriptors = maps.Clone(state.descriptors)
		state.descriptors[table.address] = &consumer
		view := e.catalog.read(&state)
		domainUnitDiagnostic(t, rowRewriteError(view, source), "0A000", `cannot alter type "pair" because column "typed.v" uses it`)
		stage := e.BeginCatalogStage(before)
		stage.typedSource = table.ofType
		stage.typedObserved = typedClosure(before, table.ofType)
		if typedObservationValid(stage, view) {
			t.Fatal("late materialized consumer escaped final observation")
		}
		e.DiscardCatalogStage(stage)
		consumer.relationKind = viewRelation
		if err := rowRewriteError(view, source); err != nil {
			t.Fatal("nonstored view blocked", err)
		}
	}
	if e.CatalogRead().state != before.state {
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
	err := stageCompositeFieldChecks(stage, source, source.attributes[0], parser.DropRestrict, nil)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop column id of composite type pair because other objects depend on it")
	if len(stage.descriptors) != 0 {
		t.Fatal("RESTRICT staged removal")
	}
	if err := stageCompositeFieldChecks(stage, source, source.attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	next := stage.descriptors[original.Address()].(*typeDescriptor)
	if len(next.domainChecks) != 1 || next.domainChecks[0].Name != "keep_check" || next.domainChecks[0].OID != original.domainChecks[1].OID || next.typ.ID() != original.typ.ID() || next.typ.DomainBase() != original.typ.DomainBase() {
		t.Fatal("wrong owner or constraint pruned")
	}
	if len(original.domainChecks) != 2 || next.typ == original.typ {
		t.Fatal("original domain authority mutated")
	}
	if !next.typ.HasDomainChecks() {
		t.Fatal("surviving CHECK lost codec authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation failed", err)
	}
	if e.CatalogRead().state != before.state {
		t.Fatal("private pruning published")
	}
	if err := stage.removeDomainCheck(next, next.domainChecks[0].OID); err != nil {
		t.Fatal(err)
	}
	if len(stage.descriptors[original.Address()].(*typeDescriptor).domainChecks) != 0 || len(next.domainChecks) != 1 {
		t.Fatal("last removal mutated surviving capture")
	}
	if stage.descriptors[original.Address()].(*typeDescriptor).typ.HasDomainChecks() {
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
	stage.typedSource = table.ofType
	stage.typedObserved = typedClosure(before, table.ofType)
	if err := stageCompositeFieldChecks(stage, source, source.attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	for _, original := range []*relationDescriptor{left, right, child} {
		pruned := stage.descriptors[original.address].(*relationDescriptor)
		if len(pruned.checkConstraints) != 1 || pruned.checkConstraints[0].Name != "keep_check" || len(original.checkConstraints) != 2 {
			t.Fatal("diamond owner pruning lost", original.name)
		}
		if _, exists := pruned.inheritance.checks["field_check"]; exists {
			t.Fatal("diamond retained retired origin", original.name)
		}
	}
	view := e.stageRead(stage)
	current := view.state.descriptors[child.address].(*relationDescriptor)
	if len(current.checkConstraints) != 1 || current.checkConstraints[0].Name != "keep_check" || len(child.checkConstraints) != 2 {
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
	if len(next.checkConstraints) != 1 || next.checkConstraints[0].Name != "keep_check" {
		t.Fatal("propagation resurrected removed check")
	}
	if _, exists := next.inheritance.checks["field_check"]; exists {
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
	if len(child.checkConstraints) != 2 || e.CatalogRead().state != before.state {
		t.Fatal("staged pruning mutated published roots")
	}
}
