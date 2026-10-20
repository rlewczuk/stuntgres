package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func numericSeriesUnit(t *testing.T, d *sqltype.Descriptor, args ...string) plan.SeriesExpr {
	t.Helper()
	e := plan.SeriesExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}}
	for _, s := range args {
		v, err := d.Input(context.Background(), s, -1)
		if err != nil {
			t.Fatal(err)
		}
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v})
	}
	return e
}

func numericSeriesUnitRead(t *testing.T, e plan.SeriesExpr) []sqltype.Value {
	t.Helper()
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	var values []sqltype.Value
	for {
		v, ok, err := cursor.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return values
		}
		values = append(values, v)
		if len(values) > 10 {
			t.Fatal("unexpected cardinality")
		}
	}
}

func TestNumericSeriesBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := nullCountRaw("generate_series", parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "numeric"}, Text: "0.1"}, parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "numeric"}, Text: "0.3"}, parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "numeric"}, Text: "0.1"})
	x, err := b.BindFunction(raw)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := x.(plan.SeriesExpr)
	if !ok || e.ResultUsage().Type.ID() != sqltype.Numeric || e.ResultUsage() != e.ResultUsage().Type.Usage() || !exec.SourceAdmitted(e) || !b.Clause.QueryHasTargetSet {
		t.Fatal(x)
	}
	want := numericSeriesUnit(t, e.ResultUsage().Type, "0.1", "0.3", "0.1")
	if !reflect.DeepEqual(numericSeriesUnitRead(t, e), numericSeriesUnitRead(t, want)) {
		t.Fatal("numeric binding values")
	}
}

func TestNumericSeriesInvocation(t *testing.T) {
	d := seriesUnitType(t, sqltype.Numeric)
	e := numericSeriesUnit(t, d, "0.1", "0.3", "0.1")
	var calls []int
	for i, arg := range e.Args {
		v := arg.(plan.LiteralExpr).Value
		e.Args[i] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { calls = append(calls, i); return v, nil })
	}
	if got := numericSeriesUnitRead(t, e); len(got) != 3 || !reflect.DeepEqual(calls, []int{0, 1, 2}) {
		t.Fatal(got, calls)
	}
	calls = nil
	e.Args[0] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { calls = append(calls, 0); return sqltype.Null(d), nil })
	if got := numericSeriesUnitRead(t, e); len(got) != 0 || !reflect.DeepEqual(calls, []int{0, 1, 2}) {
		t.Fatal(got, calls)
	}
	sentinel := errors.New("sibling evaluation")
	e.Args[1] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
	if _, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e); err != sentinel {
		t.Fatal(err)
	}
}

func TestNumericSeriesExpressionHooks(t *testing.T) {
	view := New().CatalogRead()
	d, _ := view.LookupType(sqltype.Numeric)
	original := numericSeriesUnit(t, d, "0.1", "0.3", "0.10")
	want := numericSeriesUnitRead(t, original)
	clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := numericSeriesUnitRead(t, clone.(plan.SeriesExpr)); !reflect.DeepEqual(got, want) {
		t.Fatal("remap", got)
	}
	fresh := seriesUnitType(t, sqltype.Numeric)
	mapped, changed, err := retagBoundTypes(view, original, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == d {
			return fresh
		}
		return typ
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedValues := numericSeriesUnitRead(t, mapped.(plan.SeriesExpr))
	for i, v := range mappedValues {
		restored, err := v.Retag(d)
		if err != nil || restored != want[i] {
			t.Fatal("retag", err)
		}
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil {
		t.Fatal(err)
	}
	// Bind the actual rendered expression again rather than merely checking
	// that a renderer emitted a function name.
	statements, err := parser.Parse("SELECT " + text)
	if err != nil {
		t.Fatal(err)
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	rebound, err := b.Bind(statements[0].(*parser.Select).Targets[0], expressionPolicy{Context: analyze.FunctionArgumentContext, Expected: b.Type(sqltype.Numeric).Usage()})
	if err != nil {
		t.Fatal(err)
	}
	got := numericSeriesUnitRead(t, rebound.(plan.SeriesExpr))
	if len(got) != len(want) {
		t.Fatal("rendered cardinality")
	}
	for i, v := range got {
		retagged, err := v.Retag(d)
		if err != nil || retagged != want[i] {
			t.Fatal("rendered expression", err)
		}
	}
	rewritten, changed, err := mapBoundExpression(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(n expr.Expr) (expr.Expr, bool, error) {
		if n == original.Args[0] {
			return numericSeriesUnit(t, d, "0.20", "0.3", "0.10").Args[0], true, nil
		}
		return n, false, nil
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if got := numericSeriesUnitRead(t, rewritten.(plan.SeriesExpr)); !reflect.DeepEqual(got, want[1:]) {
		t.Fatal("copy-on-change", got)
	}
	for _, fold := range []func() (expr.Expr, bool, error){
		func() (expr.Expr, bool, error) {
			return preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original)
		},
		func() (expr.Expr, bool, error) {
			return simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original, false)
		},
	} {
		n, _, err := fold()
		if err != nil {
			t.Fatal(err)
		}
		if got := numericSeriesUnitRead(t, n.(plan.SeriesExpr)); !reflect.DeepEqual(got, want) {
			t.Fatal("fold", got)
		}
	}
	if got := numericSeriesUnitRead(t, original); !reflect.DeepEqual(got, want) || original.ResultUsage().Type != d || original.Args[0].ResultUsage().Type != d {
		t.Fatal("original mutated")
	}
}
