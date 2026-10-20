package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
		capture = binder.prepared.composites[key]
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
		consumer.RelationKind = catalog.MaterializedViewRelation
		consumer.Columns = []catalog.Column{{Name: "v", Usage: typ.Usage()}}
		state := *before.State()
		state.CloneDescriptors()
		state.SetDescriptor(table.Address(), &consumer)
		view := e.catalog.read(&state)
		domainUnitDiagnostic(t, typedef.RowRewriteError(view, source), "0A000", `cannot alter type "pair" because column "typed.v" uses it`)
		stage := e.BeginCatalogStage(before)
		stage.typedState().Source = table.OfType
		stage.typedState().Observed = typedClosure(before, table.OfType)
		if typedObservationValid(stage, view) {
			t.Fatal("late materialized consumer escaped final observation")
		}
		e.DiscardCatalogStage(stage)
		consumer.RelationKind = catalog.ViewRelation
		if err := typedef.RowRewriteError(view, source); err != nil {
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
	err := stageCompositeFieldDependencies(stage, source, source.Attributes[0], parser.DropRestrict, nil)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop column id of composite type pair because other objects depend on it")
	if stage.DescriptorCount() != 0 {
		t.Fatal("RESTRICT staged removal")
	}
	if err := stageCompositeFieldDependencies(stage, source, source.Attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	next := stage.Get(original.Address()).(*catalog.Type)
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
	if len(stage.Get(original.Address()).(*catalog.Type).DomainChecks) != 0 || len(next.DomainChecks) != 1 {
		t.Fatal("last removal mutated surviving capture")
	}
	if stage.Get(original.Address()).(*catalog.Type).SQLType.HasDomainChecks() {
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
	stage.typedState().Source = table.OfType
	stage.typedState().Observed = typedClosure(before, table.OfType)
	if err := stageCompositeFieldDependencies(stage, source, source.Attributes[0], parser.DropCascade, nil); err != nil {
		t.Fatal(err)
	}
	for _, original := range []*catalog.Relation{left, right, child} {
		pruned := stage.Get(original.Address()).(*catalog.Relation)
		if len(pruned.Constraints.Check) != 1 || pruned.Constraints.Check[0].Name != "keep_check" || len(original.Constraints.Check) != 2 {
			t.Fatal("diamond owner pruning lost", original.Name())
		}
		if _, exists := pruned.Inheritance.Checks["field_check"]; exists {
			t.Fatal("diamond retained retired origin", original.Name())
		}
	}
	view := e.stageRead(stage)
	current := view.MustRelation(child.Address())
	if len(current.Constraints.Check) != 1 || current.Constraints.Check[0].Name != "keep_check" || len(child.Constraints.Check) != 2 {
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
	typedef.FinishTypedInheritance(view, &next, graph)
	if len(next.Constraints.Check) != 1 || next.Constraints.Check[0].Name != "keep_check" {
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
	if len(child.Constraints.Check) != 2 || e.CatalogRead().State() != before.State() {
		t.Fatal("staged pruning mutated published roots")
	}
}
