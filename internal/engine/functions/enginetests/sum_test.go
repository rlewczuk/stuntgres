package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sumUnitProvider(e *engine.Engine, id sqltype.ID) (functions.AggregateProvider, *sqltype.Descriptor) {
	input, _ := e.CatalogRead().LookupType(id)
	switch id {
	case sqltype.Int2, sqltype.Int4:
		result, _ := e.CatalogRead().LookupType(sqltype.Int8)
		return functions.NewIntegralSum(input, result.Usage()), input
	case sqltype.Int8, sqltype.Numeric:
		result, _ := e.CatalogRead().LookupType(sqltype.Numeric)
		return functions.NewNumericSum(input, result.Usage()), input
	default:
		return functions.NewFloatSum(input, input.Usage()), input
	}
}

func TestSumViewProviderRemapping(t *testing.T) {
	source, restored := engine.New(), engine.New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		provider, input := sumUnitProvider(source, id)
		node := &plan.AggregateNode{Child: &plan.SingletonNode{}, Calls: []plan.AggregateCall{{Provider: provider}}}
		mapped, err := stored.RemapViewQuery(restored.CatalogRead(), node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			typ, _ := restored.CatalogRead().LookupType(old.ID())
			return typ
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*plan.AggregateNode).Calls[0].Provider
		want, restoredInput := sumUnitProvider(restored, id)
		if next.ResultUsage() != want.ResultUsage() || provider.ResultUsage().Type == next.ResultUsage().Type {
			t.Fatal("result descriptor not remapped")
		}
		state, err := next.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		old, _ := input.Input(ctx, "1", -1)
		if next.Advance(ctx, state, []sqltype.Value{old, sqltype.Value{}}, nil) == nil {
			t.Fatal("restored provider retained old ownership")
		}
		for _, text := range []string{"1", "3"} {
			v, err := restoredInput.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		out, err := next.Finalize(ctx, state)
		if err != nil || out.Type() != want.ResultUsage().Type {
			t.Fatal(out, err)
		}
		text, err := out.Type().Output(ctx, out)
		if err != nil || text != "4" {
			t.Fatal(text, err)
		}
		original, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Advance(ctx, original, []sqltype.Value{old, sqltype.Value{}}, nil); err != nil {
			t.Fatal("remap mutated original", err)
		}
		newValue, _ := restoredInput.Input(ctx, "1", -1)
		if provider.Advance(ctx, original, []sqltype.Value{newValue, sqltype.Value{}}, nil) == nil {
			t.Fatal("original accepted new generation")
		}
	}
}

func TestSumSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT sum(1),sum(1),sum(1::smallint),sum(1::bigint),sum(1::numeric),sum(1::real),sum(1::double precision),avg(1),count(*) FROM t")
	defer source.Close()
	node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 8 {
		t.Fatal("input-aware immutable sharing", len(node.Calls))
	}
	for i, operation := range []functions.AggregateOperation{functions.AggregateIntegralSum, functions.AggregateIntegralSum, functions.AggregateNumericSum, functions.AggregateNumericSum, functions.AggregateFloatSum, functions.AggregateFloatSum, functions.AggregateNumericAvg, functions.AggregateCount} {
		if node.Calls[i].Operation != operation {
			t.Fatal(i, node.Calls[i].Operation)
		}
	}
	for i, c := range q.Root.Schema()[:7] {
		want := sqltype.Int8
		switch i {
		case 3, 4:
			want = sqltype.Numeric
		case 5:
			want = sqltype.Float4
		case 6:
			want = sqltype.Float8
		}
		if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
}

func TestSumEagerCancellation(t *testing.T) {
	for _, cast := range []string{"integer", "numeric", "real", "double precision"} {
		for _, phase := range []string{"advance", "finalize", "prepublication"} {
			_, q, source := queryCoreFixture(t, "SELECT sum(1::"+cast+") FROM t")
			project := q.Root.(*plan.ProjectNode)
			node := project.Child.(*plan.AggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			original := node.Calls[0].Provider
			switch phase {
			case "advance":
				node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, advance: func(ctx context.Context, s any, v, separator sqltype.Value) error {
					err := original.Advance(ctx, s, []sqltype.Value{v, separator}, nil)
					cancel()
					return err
				}}
			case "finalize":
				node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, s)
					cancel()
					return v, err
				}}
			case "prepublication":
				expr := project.Expressions[0]
				project.Expressions[0] = queryProbe(expr.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { v, err := expr.Eval(c); cancel(); return v, err })
			}
			rows, count, err := exec.Finalize(ctx, q.Root, source, nil)
			cancel()
			if rows != nil || count != 0 || err != context.Canceled || source.View != nil || source.Anonymous != nil {
				t.Fatal(cast, phase, rows, count, err)
			}
		}
	}
}

func TestSumAnalysisStructure(t *testing.T) {
	sum := parser.AggregateExpr{Name: "sum"}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	for _, tc := range []struct {
		name string
		raw  parser.Expr
		want bool
	}{
		{"nested_cast", parser.CastExpr{Child: parser.CastExpr{Child: sum}}, true},
		{"scalar_composition", parser.OperatorExpr{Args: []parser.Expr{one, parser.FunctionExpr{Args: []parser.Expr{parser.CollateExpr{Child: sum}}}}}, true},
		{"other_aggregate_child", parser.AggregateExpr{Name: "avg", Args: []parser.Expr{sum}}, true},
		{"non_sum_cast", parser.CastExpr{Child: one}, false},
		{"non_sum_aggregate", parser.AggregateExpr{Name: "avg", Args: []parser.Expr{one}}, false},
		{"nil", nil, false},
		{"child_query_only", parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{sum}}}, false},
		{"wrapped_child_query_only", parser.CastExpr{Child: parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{sum}}}}, false},
		{"outer_left_sum", parser.SubqueryExpr{Left: parser.CastExpr{Child: sum}, Query: &parser.Select{Targets: []parser.Expr{one}}}, true},
		{"outer_left_non_sum", parser.SubqueryExpr{Left: one, Query: &parser.Select{Targets: []parser.Expr{sum}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := analyze.ContainsValuesAggregateAnalysis(tc.raw); got != tc.want {
				t.Fatalf("structural VALUES aggregate containment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSumAnalysisNamespaceRestoration(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	target := &catalog.Relation{ObjectName: "sum_unit_target", Columns: []catalog.Column{{Name: "i", Usage: integer.Usage()}}}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Character: 20}}}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "sum", Pos: parser.Position{Character: 10}}}}
	for _, scope := range []string{"nil_namespace", "empty_namespace", "existing_namespace"} {
		for _, outcome := range []string{"success", "filter_failure", "missing_target_column"} {
			t.Run(scope+"/"+outcome, func(t *testing.T) {
				b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				b.Clause.AggregateClause, b.Target.InsertTarget = "INSERT VALUES", target
				hidden := []analyze.QueryQualifier{{Name: "earlier_hidden", Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "prior", Usage: integer.Usage()}}}}}
				hidden = append(hidden, analyze.QueryQualifier{Name: target.Name(), Columns: []analyze.QueryColumn{{Descriptor: target.Columns[0]}}})
				beforeHidden := append([]analyze.QueryQualifier(nil), hidden...)
				for i := range hidden {
					beforeHidden[i].Columns = append([]analyze.QueryColumn(nil), hidden[i].Columns...)
				}
				b.Query.Hidden = hidden
				var namespace *analyze.QueryNamespace
				var beforeColumns []analyze.QueryColumn
				if scope != "nil_namespace" {
					namespace = &analyze.QueryNamespace{}
					if scope == "existing_namespace" {
						namespace.Columns = []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "visible", Usage: integer.Usage()}}}
					}
					beforeColumns = append([]analyze.QueryColumn(nil), namespace.Columns...)
					b.Query.Namespace = namespace
				}
				raw := parser.AggregateExpr{Name: "sum", Pos: parser.Position{Character: 10}, Args: []parser.Expr{one}}
				switch outcome {
				case "filter_failure":
					raw.Filter = one
				case "missing_target_column":
					raw.Args = []parser.Expr{parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "i"}}}, Pos: parser.Position{Character: 20}}}
				}
				definition, _ := functions.LookupAggregate("sum")
				node, _, err := b.BindRegisteredAggregate(raw, name, definition)
				if b.Query.Namespace != namespace || len(b.Query.Hidden) != len(hidden) || &b.Query.Hidden[0] != &hidden[0] || !reflect.DeepEqual(b.Query.Hidden, beforeHidden) {
					t.Fatal("SUM leaked or replaced namespace/hidden candidates", b.Query.Namespace, b.Query.Hidden)
				}
				if namespace != nil && !reflect.DeepEqual(namespace.Columns, beforeColumns) {
					t.Fatal("SUM mutated the incumbent source namespace", namespace.Columns)
				}
				if b.Clause.AggregateClause != "INSERT VALUES" || b.Target.InsertTarget != target || len(b.Target.Uses) != 0 {
					t.Fatal("SUM changed clause/target or read diagnostic-only destination", b.Clause.AggregateClause, b.Target.InsertTarget, b.Target.Uses)
				}
				if outcome == "success" {
					if err != nil || node == nil || node.ResultUsage().Type.ID() != sqltype.Int8 {
						t.Fatal("successful bindSum path", node, err)
					}
					return
				}
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Position: "20"}
				if outcome == "filter_failure" {
					want.Code, want.Message = "42804", "argument of FILTER must be type boolean, not type integer"
				} else {
					want.Code, want.Message = "42703", `column "i" does not exist`
					want.Detail = `There is a column named "i" in table "sum_unit_target", but it cannot be referenced from this part of the query.`
				}
				if node != nil || !reflect.DeepEqual(err, want) {
					t.Fatalf("analysis result = %v / %#v, want nil / %#v", node, err, want)
				}
			})
		}
	}
}

func TestSumAnalysisAssignmentBoundary(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	column := catalog.Column{Name: "i", Usage: integer.Usage()}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Character: 30}}}
	boolean := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: parser.Position{Character: 20}}}
	sum := parser.AggregateExpr{Name: "sum", Pos: parser.Position{Character: 10}, Args: []parser.Expr{boolean}, Filter: one}
	wrapped := parser.CastExpr{Child: parser.CastExpr{Child: sum, TypeName: parser.TypeName{Name: "integer"}}, TypeName: parser.TypeName{Name: "integer"}}
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "INSERT VALUES"
	policy := analyze.ExpressionPolicy{Context: analyze.AssignmentContext, Expected: integer.Usage(), Mode: sqltype.Assignment, Column: column.Name}
	node, err := b.Bind(wrapped, policy)
	wantAnalysis := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: "30"}
	if node != nil || !analyze.ContainsValuesAggregateAnalysis(wrapped) || !reflect.DeepEqual(err, wantAnalysis) {
		t.Fatalf("wrapped nil-source analysis = %v / %#v, want %#v", node, err, wantAnalysis)
	}
	node, err = b.Bind(boolean, policy)
	if node == nil || err == nil || node.ResultUsage().Type.ID() != sqltype.Bool || analyze.ContainsValuesAggregateAnalysis(boolean) {
		t.Fatal("genuine assignment must retain its resolved source", node, err)
	}
	diagnostic := analyze.InsertValueError(e.CatalogRead(), err, column, boolean, node.ResultUsage().Type)
	wantAssignment := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "i" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "20"}
	if !reflect.DeepEqual(diagnostic, wantAssignment) {
		t.Fatalf("bound-source assignment = %#v, want %#v", diagnostic, wantAssignment)
	}
	if b.Query.Namespace != nil || len(b.Query.Hidden) != 0 || b.Clause.AggregateClause != "INSERT VALUES" {
		t.Fatal("failed wrapped analysis changed subsequent assignment scope")
	}
}
