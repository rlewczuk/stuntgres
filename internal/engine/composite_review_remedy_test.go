package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCompositeAlterFunctionalRetainedFields(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	original, _ := e.CatalogRead().LookupType(source.RowType)
	key := analyze.PreparedTypeKey{Kind: analyze.CompositeSelection}
	capture := analyze.TypeOccurrence{Kind: analyze.CompositeSelection, Base: original.Usage(), Attributes: []int32{source.Attributes[1]}}
	for _, members := range [][]sqltype.CompositeMember{{original.CompositeMember(0)}, {}, {{Attribute: 9, Name: "note", Usage: testUsage(e, sqltype.Text)}}} {
		current, err := sqltype.Composite(original.ID(), original.Name(), original.SQLName(), original.AssociatedArray(), members)
		if err != nil {
			t.Fatal(err)
		}
		binder := analyze.Binder{Host: e.AnalysisHost(nil)}
		base := plan.LiteralExpr{Info: expr.Info{Usage: current.Usage()}, Value: sqltype.Null(current)}
		node, err := binder.RetainedCompositeField(base, key, capture, parser.Position{})
		if err != nil {
			t.Fatal(err)
		}
		literal, ok := node.(plan.LiteralExpr)
		if !ok || !literal.Value.IsNull() || node.ResultUsage() != original.CompositeMember(1).Usage {
			t.Fatal("retired field retargeted", node)
		}
		capture = binder.Prepared.Types[key]
		if capture.Base.Type != original || capture.Attributes[0] != source.Attributes[1] {
			t.Fatal("original capture overwritten")
		}
	}
	binder := analyze.Binder{Host: e.AnalysisHost(nil)}
	base := plan.LiteralExpr{Info: expr.Info{Usage: original.Usage()}, Value: sqltype.Null(original)}
	node, err := binder.RetainedCompositeField(base, key, capture, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if field, ok := node.(plan.FieldExpr); !ok || field.Attribute != source.Attributes[1] {
		t.Fatal("live identity lost")
	}
}

func TestCompositeAlterMaterializedStoredGuard(t *testing.T) {
	e, source, tbl := typedUnitCatalog(t, false)
	before := e.CatalogRead()
	row, _ := before.LookupType(source.RowType)
	array, _ := before.LookupArray(row)
	domain, err := sqltype.Domain(60000, "wrapped", "public.wrapped", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{row, array, domain} {
		consumer := *tbl
		consumer.OfType = ids.ObjectAddress{}
		consumer.RelationKind = catalog.MaterializedViewRelation
		consumer.Columns = []catalog.Column{{Name: "v", Usage: typ.Usage()}}
		state := *before.State()
		state.SetDescriptor(tbl.Address(), &consumer)
		view := e.Catalog.Read(&state)
		domainUnitDiagnostic(t, typedef.RowRewriteError(view, source), "0A000", `cannot alter type "pair" because column "typed.v" uses it`)
		stage := e.BeginCatalogStage(before)
		stage.typedState().Source = tbl.OfType
		stage.typedState().Observed = table.TypedClosure(before, tbl.OfType)
		if table.TypedObservationValid(stage.Typed, view) {
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
	err := table.CompositeFieldDependencies(StageDropProvider(nil, stage, nil, nil, nil), source, source.Attributes[0], parser.DropRestrict)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop column id of composite type pair because other objects depend on it")
	if stage.DescriptorCount() != 0 {
		t.Fatal("RESTRICT staged removal")
	}
	if err := table.CompositeFieldDependencies(StageDropProvider(nil, stage, nil, nil, nil), source, source.Attributes[0], parser.DropCascade); err != nil {
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
	if err := (DDLHost{CatalogStage: stage}).RemoveDomainCheck(next, next.DomainChecks[0].OID); err != nil {
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
	e, source, tbl := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	left := domainUnitTable(t, e, op, "CREATE TABLE left_child(v pair, CONSTRAINT field_check CHECK ((v).id>0), CONSTRAINT keep_check CHECK ((v).note<>'bad')) INHERITS(typed)")
	right := domainUnitTable(t, e, op, "CREATE TABLE right_child() INHERITS(left_child)")
	child := domainUnitTable(t, e, op, "CREATE TABLE child() INHERITS(left_child,right_child)")
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	stage.typedState().Source = tbl.OfType
	stage.typedState().Observed = table.TypedClosure(before, tbl.OfType)
	if err := table.CompositeFieldDependencies(StageDropProvider(nil, stage, nil, nil, nil), source, source.Attributes[0], parser.DropCascade); err != nil {
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
	view := e.StageRead(stage)
	current := view.MustRelation(child.Address())
	if len(current.Constraints.Check) != 1 || current.Constraints.Check[0].Name != "keep_check" || len(child.Constraints.Check) != 2 {
		t.Fatal("pruning authority lost")
	}
	actions, renames, graph, err := table.TypedInheritedActions(view, current, nil)
	if err != nil || len(actions) != 0 || len(renames) != 0 {
		t.Fatal("unrelated inherited shape changed", err)
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := e.typedMutation(&StatementScope{Op: op}, op, auth, stage, current, actions)
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
