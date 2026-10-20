package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeInspectRetagTraversalAndStoredProvider(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	fresh := newTestExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	d := b.typ(sqltype.NumRange)
	v, err := d.Input(ctx, "[1.00,3.000]", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v}
	result := b.typ(sqltype.Numeric)
	original, err := functions.NewRangeInspect(b.info(result.Usage(), parser.Position{Character: 8}, expr.Properties{Volatility: expr.Immutable}), sqltype.RangeLower, []expr.Expr{arg})
	if err != nil {
		t.Fatal(err)
	}
	mapped, changed, err := retagBoundTypes(target.CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh.typ(d.ID()) })
	if err != nil || !changed {
		t.Fatal(mapped, changed, err)
	}
	next := mapped.(functions.RangeInspect)
	value, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil || value.Type() != fresh.typ(sqltype.Numeric) {
		t.Fatal(value, err)
	}
	text, err := value.Type().Output(ctx, value)
	if err != nil || text != "1.00" {
		t.Fatal(text, err)
	}
	if original.ResultUsage().Type != result || original.Args[0].ResultUsage().Type != d {
		t.Fatal("mutated retained generation")
	}
	if !sameGroupExpression(original, original) || sameGroupExpression(original, next) {
		t.Fatal("incorrect provider equivalence")
	}
	children := 0
	plan.ExpressionChildren(next, func(expr.Expr) { children++ })
	if children != 1 {
		t.Fatal("lost traversal", children)
	}
	if !generationImmutable(next) || exec.PlannedVolatility(next) != expr.Immutable || !conditionalConstantInputs(next) {
		t.Fatal("immutable carrier properties")
	}
	folded, changed, err := preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, next)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	value, err = folded.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil || value.Type() != fresh.typ(sqltype.Numeric) {
		t.Fatal("folded authority", value, err)
	}
	recipe, err := (scalarRenderer{}).scalarFunctionSQL(ctx, target.CatalogRead(), next)
	if err != nil || !strings.Contains(recipe, "pg_catalog.lower(") {
		t.Fatal(recipe, err)
	}
	forged := next
	forged.Plan = original.Plan
	if _, err := (scalarRenderer{}).scalarFunctionSQL(ctx, target.CatalogRead(), forged); err == nil {
		t.Fatal("stale stored plan")
	}
}

func TestRangeInspectBindingUsesSelectedAuthority(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	d := b.typ(sqltype.Int4Range)
	value, err := d.Input(context.Background(), "[1,4]", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lower"}}}, Pos: parser.Position{Character: 8}}
	node, err := functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Expression: arg, Usage: d.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	n, _ := got.Integer()
	if err != nil || n != 1 || got.Type() != b.typ(sqltype.Int4) {
		t.Fatal(got, err)
	}
	raw.Name.Parts[0].Text = "range_merge"
	node, err = functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Expression: arg, Usage: d.Usage()}, {Expression: arg, Usage: d.Usage()}})
	if err != nil || node.ResultUsage().Type != d {
		t.Fatal(node, err)
	}
	raw.Name.Parts[0].Text = "isempty"
	if _, err := functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Expression: arg, Usage: d.Usage()}, {Expression: arg, Usage: d.Usage()}}); err == nil {
		t.Fatal("wrong arity")
	}
}

func inspectBindingDiagnostic(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != position {
		t.Fatalf("diagnostic=%#v err=%v want %s/%s/%s", diagnostic, err, code, message, position)
	}
}

// Typed-AST units supplement the public oracle regressions without public SQL.
func TestRangeInspectTextDiagnosticEnvelope(t *testing.T) {
	ctx := context.Background()
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "AbC", Pos: parser.Position{Character: 17}}}
	for _, name := range []string{"lower", "upper"} {
		b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		base := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: parser.Position{Character: 8}}}}, Args: []parser.Expr{literal}, Pos: parser.Position{Character: 8}}
		for _, directString := range []bool{false, true} {
			bind := func(raw parser.FunctionExpr) (expr.Expr, error) {
				if directString {
					return functions.BindString(b, raw, true)
				}
				return b.bindFunctionProvider(raw)
			}
			node, err := bind(base)
			if err != nil {
				t.Fatal(err)
			}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil || value.Type() != b.typ(sqltype.Text) {
				t.Fatal(value, err)
			}
			text, err := value.Type().Output(ctx, value)
			want := "abc"
			if name == "upper" {
				want = "ABC"
			}
			if err != nil || text != want {
				t.Fatal(text, err)
			}
			for _, decoration := range []string{"DISTINCT", "ORDER BY", "FILTER", "OVER"} {
				raw := base
				kind := "not an aggregate function"
				switch decoration {
				case "DISTINCT":
					raw.Distinct = true
				case "ORDER BY":
					raw.Order = []parser.OrderKey{{Expr: literal}}
				case "FILTER":
					raw.Filter = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: false, Pos: parser.Position{Character: 31}}}
				case "OVER":
					raw.Window = &parser.WindowSpec{}
					kind = "not a window function nor an aggregate function"
				}
				node, err := bind(raw)
				if node != nil {
					t.Fatal("invalid syntax produced carrier")
				}
				inspectBindingDiagnostic(t, err, "42809", decoration+" specified, but "+name+" is "+kind, "8")
			}
			named := base
			named.ArgNames = []parser.Identifier{{Text: "x", Pos: parser.Position{Character: 14}}}
			node, err = bind(named)
			if node != nil {
				t.Fatal("named syntax produced carrier")
			}
			inspectBindingDiagnostic(t, err, "42883", "function "+name+"(x => unknown) does not exist", "8")
		}
	}
}

func TestRangeInspectChildFilterNamespaceOrdering(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.clause.aggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "absent", Pos: parser.Position{Character: 8}}, {Text: "isempty", Pos: parser.Position{Character: 15}}}}, Pos: parser.Position{Character: 8}}
	raw.Args = []parser.Expr{parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "missing", Pos: parser.Position{Character: 23}}}}, Pos: parser.Position{Character: 23}}}
	raw.Filter = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Text: "1", Pos: parser.Position{Character: 44}}}
	node, err := b.bindFunctionProvider(raw)
	if node != nil {
		t.Fatal("invalid syntax produced carrier")
	}
	inspectBindingDiagnostic(t, err, "42703", `column "missing" does not exist`, "23")
	raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Text: "1", Pos: parser.Position{Character: 23}}}}
	node, err = b.bindFunctionProvider(raw)
	if node != nil {
		t.Fatal("invalid FILTER produced carrier")
	}
	inspectBindingDiagnostic(t, err, "42804", "argument of FILTER must be type boolean, not type integer", "44")
	if b.clause.aggregateClause != "SELECT" {
		t.Fatal("FILTER analysis leaked clause", b.clause.aggregateClause)
	}
	raw.Filter = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: parser.Position{Character: 44}}}
	_, err = b.bindFunctionProvider(raw)
	inspectBindingDiagnostic(t, err, "3F000", `schema "absent" does not exist`, "8")
	raw.Name.Parts = []parser.Identifier{{Text: "lower", Pos: parser.Position{Character: 8}}}
	_, err = b.bindFunctionProvider(raw)
	inspectBindingDiagnostic(t, err, "42883", "function lower(integer) does not exist", "8")
	raw.Filter = nil
	raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "AbC"}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "AbC"}}}
	raw.ArgNames = []parser.Identifier{{Text: "x", Pos: parser.Position{Character: 14}}, {Text: "x", Pos: parser.Position{Character: 24}}}
	_, err = b.bindFunctionProvider(raw)
	inspectBindingDiagnostic(t, err, "42601", `argument name "x" used more than once`, "24")
}
