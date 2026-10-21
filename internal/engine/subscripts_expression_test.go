package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func subscriptsUnit(t *testing.T, view catalog.Read, dimensions []sqltype.ArrayDimension, dimension int64, reverse ...bool) plan.SubscriptsExpr {
	t.Helper()
	d, _ := view.LookupType(sqltype.Int4)
	array, ok := view.LookupArray(d)
	if !ok {
		t.Fatal("integer array")
	}
	count := 0
	if len(dimensions) != 0 {
		count = 1
		for _, dim := range dimensions {
			count *= int(dim.Length)
		}
	}
	elements := make([]sqltype.Value, count)
	for i := range elements {
		elements[i] = sqltype.Null(d)
	}
	value, err := sqltype.ArrayValue(array, dimensions, elements)
	if err != nil {
		t.Fatal(err)
	}
	integer, err := sqltype.Integer(d, dimension)
	if err != nil {
		t.Fatal(err)
	}
	e := plan.SubscriptsExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: value},
		plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: integer},
	}}
	if len(reverse) != 0 {
		boolean, _ := view.LookupType(sqltype.Bool)
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, reverse[0])})
	}
	return e
}

func subscriptsUnitRead(t *testing.T, e plan.SubscriptsExpr) []int64 {
	t.Helper()
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	var got []int64
	for {
		v, present, err := cursor.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			if !v.IsNull() || v.Type() != e.ResultUsage().Type {
				t.Fatal("untyped EOF", v)
			}
			return got
		}
		n, err := v.Integer()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
		if len(got) > 10 {
			t.Fatal("nonterminating bounds", got)
		}
	}
}

func TestSubscriptsBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	array := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{0}}, Syntax: parser.PostfixCast}
	integer := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	x, err := b.BindFunction(nullCountRaw("generate_subscripts", array, integer, parser.ParameterExpr{Number: 2}))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := x.(plan.SubscriptsExpr)
	if !ok || e.ResultUsage().Type.ID() != sqltype.Int4 || !e.Properties().StageDependent || !e.Properties().ParameterDependent || !b.Clause.QueryHasTargetSet || !exec.SourceAdmitted(e) {
		t.Fatal(x)
	}
	if b.Params.Parameters[1].Type == nil || !b.Params.Parameters[1].Type.IsArray() || b.Params.Parameters[2].Type.ID() != sqltype.Bool {
		t.Fatal("parameter identity", b.Params.Parameters)
	}
	if _, err := e.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("unlifted set scalar evaluation")
	}
	_, err = b.BindFunction(nullCountRaw("generate_subscripts", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, integer))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	_, err = b.BindFunction(nullCountRaw("generate_subscripts", array, parser.CastExpr{Child: integer, TypeName: parser.TypeName{Name: "bigint"}}))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
		t.Fatal("implicit narrowing accepted", err)
	}
}

func TestSubscriptsCursor(t *testing.T) {
	view := New().CatalogRead()
	dims := []sqltype.ArrayDimension{{Lower: -1, Length: 2}, {Lower: 3, Length: 3}}
	for _, tc := range []struct {
		dimension int64
		reverse   bool
		want      []int64
	}{
		{1, false, []int64{-1, 0}}, {1, true, []int64{0, -1}}, {2, false, []int64{3, 4, 5}}, {2, true, []int64{5, 4, 3}},
		{0, false, nil}, {-1, true, nil}, {3, false, nil}, {math.MinInt32, true, nil}, {math.MaxInt32, false, nil},
	} {
		if got := subscriptsUnitRead(t, subscriptsUnit(t, view, dims, tc.dimension, tc.reverse)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got)
		}
	}
	if got := subscriptsUnitRead(t, subscriptsUnit(t, view, nil, 1)); got != nil {
		t.Fatal("rank zero", got)
	}
	for nullArg := range 3 {
		e := subscriptsUnit(t, view, dims, 2, true)
		order := []int{}
		for i, arg := range e.Args {
			value := arg.(plan.LiteralExpr).Value
			if i == nullArg {
				value = sqltype.Null(value.Type())
			}
			e.Args[i] = queryProbe(value.Type().Usage(), func(*exec.Context) (sqltype.Value, error) { order = append(order, i); return value, nil })
		}
		if got := subscriptsUnitRead(t, e); got != nil || !reflect.DeepEqual(order, []int{0, 1, 2}) {
			t.Fatal("strict call skipped/repeated child", got, order)
		}
	}
	e := subscriptsUnit(t, view, dims, 2, true)
	sentinel := errors.New("later child")
	e.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: e.Args[0].ResultUsage()}, Value: sqltype.Null(e.Args[0].ResultUsage().Type)}
	e.Args[1] = queryProbe(e.Args[1].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
	e.Args[2] = queryProbe(e.Args[2].ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
		t.Fatal("evaluation continued after child error")
		return sqltype.Value{}, nil
	})
	if _, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e); err != sentinel {
		t.Fatal(err)
	}
}

func TestSubscriptsBounds(t *testing.T) {
	view := New().CatalogRead()
	for _, tc := range []struct {
		lower   int32
		reverse bool
		want    []int64
	}{
		{math.MinInt32, true, []int64{math.MinInt32 + 1, math.MinInt32}},
		{math.MinInt32, false, []int64{math.MinInt32, math.MinInt32 + 1}},
		{math.MaxInt32 - 2, true, []int64{math.MaxInt32 - 1, math.MaxInt32 - 2}},
		{math.MaxInt32 - 2, false, []int64{math.MaxInt32 - 2, math.MaxInt32 - 1}},
	} {
		if got := subscriptsUnitRead(t, subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: tc.lower, Length: 2}}, 1, tc.reverse)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got)
		}
	}
	e := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: -2, Length: 3}}, 1)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	first, err := exec.OpenFunctionSource(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := exec.OpenFunctionSource(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, tc := range []struct {
		cursor *expr.SourceCursor
		want   int64
	}{{&first, -2}, {&first, -1}, {&second, -2}} {
		v, ok, err := tc.cursor.Next(c.Context)
		n, _ := v.Integer()
		if err != nil || !ok || n != tc.want {
			t.Fatal("shared invocation state", n, tc.want, err)
		}
	}
	if got := subscriptsUnitRead(t, e); !reflect.DeepEqual(got, []int64{-2, -1, 0}) {
		t.Fatal("marker mutated", got)
	}
}

func TestSubscriptsExpressionHooks(t *testing.T) {
	view := New().CatalogRead()
	original := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 3, Length: 3}}, 1, true)
	freshView := New().CatalogRead()
	mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		fresh, ok := freshView.LookupType(d.ID())
		if ok {
			return fresh
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(plan.SubscriptsExpr)
	if next.ResultUsage().Type == original.ResultUsage().Type || next.Args[0].ResultUsage().Type == original.Args[0].ResultUsage().Type || next.Properties() != original.Properties() || next.SourcePosition() != original.SourcePosition() {
		t.Fatal("retag lost immutable descriptor closure")
	}
	if got := subscriptsUnitRead(t, next); !reflect.DeepEqual(got, []int64{5, 4, 3}) {
		t.Fatal(got)
	}
	walked := 0
	plan.WalkExpression(original, func(n expr.Expr) {
		if _, ok := n.(plan.LiteralExpr); ok {
			walked++
		}
	})
	if walked != 3 || exec.PlannedVolatility(original) != expr.Immutable || exec.QuerySetDepth(original) != 1 || !analyze.SameGroupExpression(original, original) {
		t.Fatal("visitor identity", walked)
	}
	other := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 3, Length: 3}}, 1, false)
	if analyze.SameGroupExpression(original, other) || analyze.SameQuerySetOccurrence(original, seriesUnit(t, original.ResultUsage().Type, 1, 3)) {
		t.Fatal("marker/argument kinds conflated")
	}
	clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !analyze.SameGroupExpression(original, clone) {
		t.Fatal("remap", clone, err)
	}
	for _, fold := range []func() (expr.Expr, bool, error){
		func() (expr.Expr, bool, error) {
			return preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}, original)
		},
		func() (expr.Expr, bool, error) {
			return simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}, original, false)
		},
	} {
		n, _, err := fold()
		if err != nil || !exec.SourceReturnsSet(n) {
			t.Fatal(n, err)
		}
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil || !strings.Contains(text, "generate_subscripts(") || !strings.Contains(text, "ARRAY") && !strings.Contains(text, "{NULL") {
		t.Fatal("symbolic deparse", text, err)
	}
	tree, err := (plan.BoundTree{Expression: original}).StructuralOutput(context.Background())
	if err != nil || !strings.Contains(tree, "Subscripts generate_subscripts") || strings.Count(tree, "node Const") != 3 {
		t.Fatal(tree, err)
	}
	volatile := original
	volatile.Args = append([]expr.Expr(nil), original.Args...)
	probe := queryProbe(original.Args[2].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, nil }).(queryProbeExpression)
	probe.Property.Volatility = expr.Volatile
	volatile.Args[2] = probe
	if exec.PlannedVolatility(volatile) != expr.Volatile {
		t.Fatal("reverse child volatility erased")
	}
	replacement := other.Args[2]
	n, did, err := expr.MapSubstituted(original, func(arg expr.Expr) (expr.Expr, bool, error) {
		if arg == original.Args[2] {
			return replacement, true, nil
		}
		return arg, false, nil
	})
	if err != nil || !did || !analyze.SameGroupExpression(n, other) || analyze.SameGroupExpression(original, other) {
		t.Fatal("copy-on-change", n, did, err)
	}
	if _, err := expr.Substitute(expr.Expr(original).(expr.Composite), expr.InfoOf(original), original.Args[:2]); err == nil {
		t.Fatal("unchecked argument replacement")
	}
}

func subscriptsUnitProject(t *testing.T, engine *Engine, bound plan.SubscriptsExpr) *plan.ProjectNode {
	t.Helper()
	root, outputs, err := regexpSetUnitLower(engine, &plan.SingletonNode{}, []expr.Expr{bound}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "s", Usage: bound.ResultUsage()}}, Child: root, Expressions: outputs, Frame: 1, HasTargetSet: true}
}

func TestSubscriptsLowering(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	matrix := []sqltype.ArrayDimension{{Lower: -1, Length: 2}, {Lower: 3, Length: 3}}
	e := subscriptsUnit(t, view, matrix, 1)
	e.Args[1] = seriesUnit(t, d, 1, 2)
	q := subscriptsUnitProject(t, engine, e)
	r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
	if err != nil || count != 5 {
		t.Fatal(count, err)
	}
	defer r.Close()
	var got []int64
	row := make([]sqltype.Value, 1)
	for {
		ok, err := r.Next(context.Background(), row)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		n, _ := row[0].Integer()
		got = append(got, n)
	}
	if !reflect.DeepEqual(got, []int64{-1, 0, 3, 4, 5}) {
		t.Fatal("nested invocation", got)
	}
	for _, limit := range []int64{0, 2} {
		e := subscriptsUnit(t, view, matrix, 2)
		opens := 0
		q := subscriptsUnitProject(t, engine, e)
		arg := e.Args[0].(plan.LiteralExpr).Value
		functionSetUnitArgument(t, q.Child.(*plan.SetProjectNode).Calls, 0, 0, queryProbe(arg.Type().Usage(), func(*exec.Context) (sqltype.Value, error) { opens++; return arg, nil }))
		q.Child = &plan.LimitNode{QuerySchema: q.Child.Schema(), Child: q.Child, Count: seriesUnit(t, d, limit, limit).Args[0]}
		r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
		wantOpens := 1
		if limit == 0 {
			wantOpens = 0
		}
		if err != nil || int64(count) != limit || opens != wantOpens {
			t.Fatal("LIMIT demand", count, opens, err)
		}
		r.Close()
	}
}

func TestSubscriptsCancellation(t *testing.T) {
	view := New().CatalogRead()
	e := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 3, Length: 3}}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cursor.Next(ctx); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cancel()
	if _, ok, err := cursor.Next(ctx); ok || err != context.Canceled {
		t.Fatal(ok, err)
	}
	cursor.Close()
	cursor.Close()
	if !cursor.Exhausted || cursor.Values != nil {
		t.Fatal("cursor cleanup")
	}
	calls := 0
	e.Args[0] = queryProbe(e.Args[0].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, nil })
	if _, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, e); err != context.Canceled || calls != 0 {
		t.Fatal("pre-admission cancellation", calls, err)
	}
}

func TestSubscriptsFinalization(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	e := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 1, Length: 3}}, 1)
	t.Run("later-invocation", func(t *testing.T) {
		calls := 0
		sentinel := errors.New("later subscripts argument")
		value := e.Args[0].(plan.LiteralExpr).Value
		input := &plan.ValuesNode{Rows: [][]expr.Expr{{}, {}}}
		root, outputs, err := regexpSetUnitLower(engine, input, []expr.Expr{e}, 1)
		if err != nil {
			t.Fatal(err)
		}
		functionSetUnitArgument(t, root.(*plan.SetProjectNode).Calls, 0, 0, queryProbe(value.Type().Usage(), func(*exec.Context) (sqltype.Value, error) {
			calls++
			if calls == 2 {
				return sqltype.Value{}, sentinel
			}
			return value, nil
		}))
		q := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: d.Usage()}}, Child: root, Expressions: outputs, Frame: 1, HasTargetSet: true}
		r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
		if r != nil || count != 0 || err != sentinel || calls != 2 {
			t.Fatal("partial publication", r, count, err, calls)
		}
	})
	for _, phase := range []string{"error", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			q := subscriptsUnitProject(t, engine, e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			sentinel := errors.New("late subscripts projection")
			q.Expressions = []expr.Expr{queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 2 {
					if phase == "cancel" {
						cancel()
					} else {
						return sqltype.Value{}, sentinel
					}
				}
				return sqltype.Integer(d, 1)
			})}
			r, count, err := exec.Finalize(ctx, q, &exec.Source{Materialized: true}, nil)
			want := sentinel
			if phase == "cancel" {
				want = context.Canceled
			}
			if r != nil || count != 0 || err != want || calls < 2 {
				t.Fatal("partial eager publication", r, count, err, calls)
			}
		})
	}
}
