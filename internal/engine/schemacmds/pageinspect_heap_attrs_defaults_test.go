package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPageinspectHeapAttrsDefaultClosure(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, explicit := range []bool{false, true} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "tuple_data_split"}}}}
		values := []string{"0", "\\x", "0", "0", ""}
		if explicit {
			values = append(values, "false")
		}
		for _, value := range values {
			raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}})
		}
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledPageinspectExpr)
		if !analyze.ClosedPageinspectDefault(n) || !n.Properties().StageDependent || n.Properties().Volatility != expr.Volatile || n.PlanningFold() != expr.FoldNever {
			t.Fatal("split default lost intrinsic live properties", n)
		}
		if n.ScalarSourceOutName() != "" || n.Pageinspect.ScalarSourceOutName() != "" {
			t.Fatal("unnamed scalar output overrode range alias")
		}
		persisted, err := b.BindDefault(raw, catalog.Column{Name: "attrs", Usage: n.ResultUsage()})
		if err != nil {
			t.Fatal("bounded splitter default", err)
		}
		if !analyze.ClosedPageinspectDefault(persisted) || !persisted.Properties().StageDependent || persisted.Properties().Volatility != expr.Volatile {
			t.Fatal("stored default masked live dependence")
		}
		if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), e.CatalogRead(), persisted); err != nil {
			t.Fatal("genuine default rendering", err)
		}
		for _, property := range []expr.Properties{{RowDependent: true}, {ParameterDependent: true}} {
			bad := n
			bad.Property = expr.MergeProperties(n.Properties(), property)
			if analyze.ClosedPageinspectDefault(bad) {
				t.Fatal("dependent split default admitted", property)
			}
		}
		for _, property := range []expr.Properties{{StageDependent: true}, {Volatility: expr.Volatile}} {
			children := append([]expr.Expr(nil), n.Args...)
			usage := children[0].ResultUsage()
			children[0] = plan.LiteralExpr{Info: expr.Info{Usage: usage, Property: property}, Value: sqltype.Null(usage.Type)}
			rebuilt, err := n.Rebuild(n.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			if analyze.ClosedPageinspectDefault(rebuilt) {
				t.Fatal("unrelated dependent/volatile child admitted", property)
			}
		}
		forged := n
		p := *n.Procedure
		p.Implementation = new(extension.PageinspectProvider)
		forged.Procedure, forged.RoutineReference = &p, &p
		if analyze.ClosedPageinspectDefault(forged) {
			t.Fatal("foreign splitter provider admitted")
		}
	}
}

func TestPageinspectHeapAttrsDeclarationDependencies(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	view := e.CatalogRead()
	for _, descriptor := range []struct {
		slot    int
		helpers []int
	}{{19, []int{15, 18}}, {20, []int{19}}} {
		installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pageinspect"})).Installation
		routine := view.MustProcedure(installation.Members[descriptor.slot])
		members := routine.Origin.Installation.Members
		seen := map[ids.ObjectAddress]int{}
		catalog.VisitProcedureCapturedDefinitionDependencies(view, routine, func(address ids.ObjectAddress) bool { seen[address]++; return true })
		if len(seen) != len(descriptor.helpers) {
			t.Fatal("wrapper dependency width", seen)
		}
		for _, helper := range descriptor.helpers {
			if seen[members[helper]] != 1 {
				t.Fatal("missing/duplicate genuine helper edge", helper, seen)
			}
		}
		visited := 0
		if extension.StockPageinspect.VisitProcedureDependencies(view, routine, func(ids.ObjectAddress) bool { visited++; return false }) || visited != 1 {
			t.Fatal("dependency visitor ignored early stop", visited)
		}
	}
}

func TestPageinspectHeapAttrsStoredCheckStage(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE attrs_check_owner(v bytea[])")
	view := e.CatalogRead()
	var owner *catalog.Relation
	for _, relation := range view.Relations() {
		if relation.Name() == "attrs_check_owner" {
			owner = relation
			break
		}
	}
	if owner == nil {
		t.Fatal("missing genuine CHECK owner")
	}
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "tuple_data_split"}}}}
	for _, value := range []string{"0", "\\x", "0", "0", ""} {
		raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}})
	}
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledPageinspectExpr)
	boolean := b.Type(sqltype.Bool).Usage()
	left := plan.NullTestExpr{Info: expr.Info{Usage: boolean, Property: call.Properties()}, Child: call, Not: true}
	ref := view.Owner().Domain.Ref(view.Epoch(), owner.Address())
	ref.Address.SubID = owner.Attributes[0]
	column := plan.ColumnExpr{Info: expr.Info{Usage: owner.Columns[0].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: 0}
	right := plan.NullTestExpr{Info: expr.Info{Usage: boolean, Property: column.Properties()}, Child: column, Not: true}
	check := plan.LogicalExpr{Info: expr.Info{Usage: boolean, Property: expr.MergeProperties(left.Properties(), right.Properties())}, Op: parser.LogicalAnd, Left: left, Right: right}
	if !analyze.PageinspectStoredStage(check) || analyze.ClosedPageinspectDefault(check) {
		t.Fatal("CHECK/default ownership distinction")
	}
	if text, err := (stored.ScalarRenderer{Owner: owner}).SQL(context.Background(), view, check); err != nil || text == "" {
		t.Fatal("actual owning CHECK rendering", text, err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, check); err == nil {
		t.Fatal("row-dependent CHECK admitted as ownerless default")
	}
	// A closed Boolean wrapper is an admissible default without dropping the
	// splitter's live dependence; row-owned siblings remain forbidden there.
	if !analyze.ClosedPageinspectDefault(left) {
		t.Fatal("closed NULL-test wrapper rejected")
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, left); err != nil {
		t.Fatal("closed Boolean default rendering", err)
	}
	for _, property := range []expr.Properties{{StageDependent: true}, {ParameterDependent: true}} {
		badSibling := plan.LiteralExpr{Info: expr.Info{Usage: boolean, Property: property}, Value: sqltype.Boolean(boolean.Type, true)}
		mixed := check
		mixed.Right = badSibling
		mixed.Property = expr.MergeProperties(left.Properties(), badSibling.Properties())
		if analyze.PageinspectStoredStage(mixed) || analyze.ClosedPageinspectDefault(mixed) {
			t.Fatal("genuine split laundered unrelated sibling dependence", property)
		}
		if _, err := (stored.ScalarRenderer{Owner: owner}).SQL(context.Background(), view, mixed); err == nil {
			t.Fatal("mixed-stage/parameter CHECK rendered", property)
		}
	}
	forged := call
	procedure := *call.Procedure
	procedure.Display.Source = "forged"
	forged.Procedure, forged.RoutineReference = &procedure, &procedure
	bad := left
	bad.Child = forged
	if analyze.PageinspectStoredStage(bad) || analyze.ClosedPageinspectDefault(bad) {
		t.Fatal("forged stage producer admitted")
	}
}
