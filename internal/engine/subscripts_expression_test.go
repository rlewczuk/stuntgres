package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func subscriptsUnit(t *testing.T, view CatalogRead, dimensions []sqltype.ArrayDimension, dimension int64, reverse ...bool) subscriptsExpression {
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
	e := subscriptsExpression{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}, args: []expr.Expr{
		literalExpression{Info: expr.Info{Usage: array.Usage()}, value: value},
		literalExpression{Info: expr.Info{Usage: d.Usage()}, value: integer},
	}}
	if len(reverse) != 0 {
		boolean, _ := view.LookupType(sqltype.Bool)
		e.args = append(e.args, literalExpression{Info: expr.Info{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, reverse[0])})
	}
	return e
}

func subscriptsUnitRead(t *testing.T, e subscriptsExpression) []int64 {
	t.Helper()
	cursor, err := openFunctionCall(&EvalContext{Context: context.Background()}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.close()
	var got []int64
	for {
		v, present, err := cursor.next(context.Background(), e.ResultUsage().Type)
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
	b.aggregateClause = "SELECT"
	array := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{0}}, Syntax: parser.PostfixCast}
	integer := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	x, err := b.bindFunction(nullCountRaw("generate_subscripts", array, integer, parser.ParameterExpr{Number: 2}))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := x.(subscriptsExpression)
	if !ok || e.ResultUsage().Type.ID() != sqltype.Int4 || !e.Properties().StageDependent || !e.Properties().ParameterDependent || !b.queryHasTargetSet || !validFunctionSourceCall(e) {
		t.Fatal(x)
	}
	if b.parameters[1].Type == nil || !b.parameters[1].Type.IsArray() || b.parameters[2].Type.ID() != sqltype.Bool {
		t.Fatal("parameter identity", b.parameters)
	}
	if _, err := e.Eval(&EvalContext{Context: context.Background()}); err == nil {
		t.Fatal("unlifted set scalar evaluation")
	}
	_, err = b.bindFunction(nullCountRaw("generate_subscripts", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, integer))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	_, err = b.bindFunction(nullCountRaw("generate_subscripts", array, parser.CastExpr{Child: integer, TypeName: parser.TypeName{Name: "bigint"}}))
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
		for i, arg := range e.args {
			value := arg.(literalExpression).value
			if i == nullArg {
				value = sqltype.Null(value.Type())
			}
			e.args[i] = queryProbe(value.Type().Usage(), func(*EvalContext) (sqltype.Value, error) { order = append(order, i); return value, nil })
		}
		if got := subscriptsUnitRead(t, e); got != nil || !reflect.DeepEqual(order, []int{0, 1, 2}) {
			t.Fatal("strict call skipped/repeated child", got, order)
		}
	}
	e := subscriptsUnit(t, view, dims, 2, true)
	sentinel := errors.New("later child")
	e.args[0] = literalExpression{Info: expr.Info{Usage: e.args[0].ResultUsage()}, value: sqltype.Null(e.args[0].ResultUsage().Type)}
	e.args[1] = queryProbe(e.args[1].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
	e.args[2] = queryProbe(e.args[2].ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("evaluation continued after child error")
		return sqltype.Value{}, nil
	})
	if _, err := openFunctionCall(&EvalContext{Context: context.Background()}, e); err != sentinel {
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
	c := &EvalContext{Context: context.Background()}
	first, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	for _, tc := range []struct {
		cursor *functionSetCursor
		want   int64
	}{{&first, -2}, {&first, -1}, {&second, -2}} {
		v, ok, err := tc.cursor.next(c.Context, e.ResultUsage().Type)
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
	next := mapped.(subscriptsExpression)
	if next.ResultUsage().Type == original.ResultUsage().Type || next.args[0].ResultUsage().Type == original.args[0].ResultUsage().Type || next.Properties() != original.Properties() || next.SourcePosition() != original.SourcePosition() {
		t.Fatal("retag lost immutable descriptor closure")
	}
	if got := subscriptsUnitRead(t, next); !reflect.DeepEqual(got, []int64{5, 4, 3}) {
		t.Fatal(got)
	}
	walked := 0
	walkBoundExpression(original, func(n expr.Expr) {
		if _, ok := n.(literalExpression); ok {
			walked++
		}
	})
	if walked != 3 || plannedVolatility(original) != expr.Immutable || querySetDepth(original) != 1 || !sameGroupExpression(original, original) {
		t.Fatal("visitor identity", walked)
	}
	other := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 3, Length: 3}}, 1, false)
	if sameGroupExpression(original, other) || sameQuerySetOccurrence(original, seriesUnit(t, original.ResultUsage().Type, 1, 3)) {
		t.Fatal("marker/argument kinds conflated")
	}
	clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !sameGroupExpression(original, clone) {
		t.Fatal("remap", clone, err)
	}
	for _, fold := range []func() (expr.Expr, bool, error){
		func() (expr.Expr, bool, error) {
			return preflightCoercions(&EvalContext{Context: context.Background(), catalog: view}, original)
		},
		func() (expr.Expr, bool, error) {
			return simplifyConditional(&EvalContext{Context: context.Background(), catalog: view}, original, false)
		},
	} {
		n, _, err := fold()
		if err != nil || !isQuerySet(n) {
			t.Fatal(n, err)
		}
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil || !strings.Contains(text, "generate_subscripts(") || !strings.Contains(text, "ARRAY") && !strings.Contains(text, "{NULL") {
		t.Fatal("symbolic deparse", text, err)
	}
	tree, err := (boundTree{expression: original}).StructuralOutput(context.Background())
	if err != nil || !strings.Contains(tree, "Subscripts generate_subscripts") || strings.Count(tree, "node Const") != 3 {
		t.Fatal(tree, err)
	}
	volatile := original
	volatile.args = append([]expr.Expr(nil), original.args...)
	probe := queryProbe(original.args[2].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, nil }).(queryProbeExpression)
	probe.Property.Volatility = expr.Volatile
	volatile.args[2] = probe
	if plannedVolatility(volatile) != expr.Volatile {
		t.Fatal("reverse child volatility erased")
	}
	replacement := other.args[2]
	n, did, err := original.mapChildren(func(arg expr.Expr) (expr.Expr, bool, error) {
		if arg == original.args[2] {
			return replacement, true, nil
		}
		return arg, false, nil
	})
	if err != nil || !did || !sameGroupExpression(n, other) || sameGroupExpression(original, other) {
		t.Fatal("copy-on-change", n, did, err)
	}
	if _, err := withFunctionCallArguments(original, original.args[:2]); err == nil {
		t.Fatal("unchecked argument replacement")
	}
}

func subscriptsUnitProject(t *testing.T, engine *Engine, bound subscriptsExpression) *projectNode {
	t.Helper()
	root, outputs, err := regexpSetUnitLower(engine, &singletonNode{}, []expr.Expr{bound}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return &projectNode{querySchema: querySchema{{Name: "s", Usage: bound.ResultUsage()}}, child: root, expressions: outputs, frame: 1, hasTargetSet: true}
}

func TestSubscriptsLowering(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	matrix := []sqltype.ArrayDimension{{Lower: -1, Length: 2}, {Lower: 3, Length: 3}}
	e := subscriptsUnit(t, view, matrix, 1)
	e.args[1] = seriesUnit(t, d, 1, 2)
	q := subscriptsUnitProject(t, engine, e)
	r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
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
		arg := e.args[0].(literalExpression).value
		functionSetUnitArgument(t, q.child.(*setProjectNode).calls, 0, 0, queryProbe(arg.Type().Usage(), func(*EvalContext) (sqltype.Value, error) { opens++; return arg, nil }))
		q.child = &limitNode{querySchema: q.child.Schema(), child: q.child, count: seriesUnit(t, d, limit, limit).args[0]}
		r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
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
	cursor, err := openFunctionCall(&EvalContext{Context: ctx}, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cursor.next(ctx, e.ResultUsage().Type); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cancel()
	if _, ok, err := cursor.next(ctx, e.ResultUsage().Type); ok || err != context.Canceled {
		t.Fatal(ok, err)
	}
	cursor.close()
	cursor.close()
	if !cursor.exhausted || cursor.owned != nil {
		t.Fatal("cursor cleanup")
	}
	calls := 0
	e.args[0] = queryProbe(e.args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Value{}, nil })
	if _, err := openFunctionCall(&EvalContext{Context: ctx}, e); err != context.Canceled || calls != 0 {
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
		value := e.args[0].(literalExpression).value
		input := &valuesNode{rows: [][]expr.Expr{{}, {}}}
		root, outputs, err := regexpSetUnitLower(engine, input, []expr.Expr{e}, 1)
		if err != nil {
			t.Fatal(err)
		}
		functionSetUnitArgument(t, root.(*setProjectNode).calls, 0, 0, queryProbe(value.Type().Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls++
			if calls == 2 {
				return sqltype.Value{}, sentinel
			}
			return value, nil
		}))
		q := &projectNode{querySchema: querySchema{{Usage: d.Usage()}}, child: root, expressions: outputs, frame: 1, hasTargetSet: true}
		r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
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
			q.expressions = []expr.Expr{queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) {
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
			r, count, err := finalizeQuery(ctx, q, &querySource{materialized: true}, nil)
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
