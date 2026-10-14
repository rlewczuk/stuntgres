package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func seriesUnit(t *testing.T, d *sqltype.Descriptor, values ...int64) seriesExpression {
	t.Helper()
	e := seriesExpression{expressionInfo: expressionInfo{usage: d.Usage(), pos: parser.Position{Character: 8, Offset: 7}, properties: ExprProperties{StageDependent: true}}}
	for _, n := range values {
		v, err := sqltype.Integer(d, n)
		if err != nil {
			t.Fatal(err)
		}
		e.args = append(e.args, literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: v})
	}
	return e
}
func seriesUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d
		}
	}
	t.Fatalf("missing builtin descriptor %d", id)
	return nil
}
func seriesUnitRead(t *testing.T, e seriesExpression) []int64 {
	t.Helper()
	cursor, err := openFunctionCall(&EvalContext{Context: context.Background()}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.close()
	var out []int64
	for {
		v, ok, err := cursor.next(context.Background(), e.ResultUsage().Type)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			if !v.IsNull() || v.Type() != e.ResultUsage().Type {
				t.Fatal(v)
			}
			return out
		}
		n, err := v.Integer()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
		if len(out) > 10 {
			t.Fatal("unexpected series length")
		}
	}
}

func TestSeriesBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	lit := func(n int64) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
	}
	x, err := b.bindFunction(nullCountRaw("generate_series", lit(1), lit(3)))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := x.(seriesExpression)
	if !ok || e.ResultUsage().Type.ID() != sqltype.Int4 || !e.Properties().StageDependent || !b.queryHasTargetSet || !validFunctionSourceCall(e) {
		t.Fatal(x)
	}
	if _, err = e.Eval(&EvalContext{Context: context.Background()}); err == nil {
		t.Fatal("scalar consumed set")
	}
	_, err = b.bindFunction(nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42725" {
		t.Fatal(err)
	}
	_, err = b.bindFunction(nullCountRaw("generate_series",
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "date"}, Text: "2020-01-01"},
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "date"}, Text: "2020-01-02"},
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "interval"}, Text: "1 day"}))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "generate_series with temporal arguments is unsupported" {
		t.Fatal("DATE category fabricated temporal ambiguity", err)
	}
}

func TestSeriesCursor(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	for _, tc := range []struct{ args, want []int64 }{
		{[]int64{1, 3}, []int64{1, 2, 3}}, {[]int64{5, 1, -2}, []int64{5, 3, 1}}, {[]int64{1, 6, 2}, []int64{1, 3, 5}}, {[]int64{3, 3, -2}, []int64{3}}, {[]int64{3, 1}, nil},
	} {
		if got := seriesUnitRead(t, seriesUnit(t, d, tc.args...)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.args, got, tc.want)
		}
	}
	e := seriesUnit(t, d, 1, 3, 0)
	_, err := openFunctionCall(&EvalContext{Context: context.Background()}, e)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "step size cannot equal zero" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	calls := 0
	e.args[0] = literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: sqltype.Null(d)}
	e.args[1] = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Integer(d, 3) })
	if got := seriesUnitRead(t, e); len(got) != 0 || calls != 1 {
		t.Fatal(got, calls)
	}
	sentinel := errors.New("sibling error")
	e.args[1] = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
	if _, err := openFunctionCall(&EvalContext{Context: context.Background()}, e); err != sentinel {
		t.Fatal(err)
	}
}

func TestSeriesOverflow(t *testing.T) {
	for _, tc := range []struct {
		id         sqltype.ID
		args, want []int64
	}{
		{sqltype.Int4, []int64{math.MaxInt32 - 1, math.MaxInt32, 2}, []int64{math.MaxInt32 - 1}},
		{sqltype.Int4, []int64{math.MinInt32 + 1, math.MinInt32, -2}, []int64{math.MinInt32 + 1}},
		{sqltype.Int8, []int64{math.MaxInt64, math.MaxInt64, 1}, []int64{math.MaxInt64}},
		{sqltype.Int8, []int64{math.MinInt64, math.MinInt64, -1}, []int64{math.MinInt64}},
		{sqltype.Int8, []int64{0, math.MinInt64, math.MinInt64}, []int64{0, math.MinInt64}},
		{sqltype.Int8, []int64{-1, math.MaxInt64, math.MaxInt64}, []int64{-1, math.MaxInt64 - 1}},
	} {
		if got := seriesUnitRead(t, seriesUnit(t, seriesUnitType(t, tc.id), tc.args...)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.args, got)
		}
	}
}

func TestSeriesExpressionHooks(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	original := seriesUnit(t, d, 1, 3, 1)
	t.Run("strict-arithmetic-constant-null", func(t *testing.T) {
		plan, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, d, d, d)
		if err != nil {
			t.Fatal(err)
		}
		null := literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: sqltype.Null(d)}
		wrapper := arithmeticExpression{expressionInfo: original.expressionInfo, left: original, right: null, provider: plan, operator: sqltype.ArithmeticAdd}
		ctx := &EvalContext{Context: context.Background(), catalog: view}
		for _, fold := range []func() (BoundExpr, bool, error){
			func() (BoundExpr, bool, error) { return preflightCoercions(ctx, wrapper) },
			func() (BoundExpr, bool, error) { return simplifyConditional(ctx, wrapper, false) },
		} {
			node, changed, err := fold()
			value, ok := node.(literalExpression)
			if err != nil || !changed || !ok || !value.value.IsNull() || value.ResultUsage() != wrapper.ResultUsage() || querySetDepth(node) != 0 {
				t.Fatal("strict set wrapper retained cardinality", node, changed, err)
			}
		}
		if querySetDepth(wrapper) != 1 || querySetDepth(original) != 1 {
			t.Fatal("mutated analyzed marker")
		}
		parameter := null
		parameter.properties.ParameterDependent = true
		retained := wrapper
		retained.right = parameter
		if arithmeticSetConstantNull(retained, retained) {
			t.Fatal("imported parameter became a physical NULL proof")
		}
		peer := seriesUnit(t, d, 10, 11)
		peer.pos = parser.Position{Offset: 20, Character: 21}
		root, _, err := regexpSetUnitLower(view, &singletonNode{}, []BoundExpr{wrapper, peer}, 1)
		if err != nil {
			t.Fatal(err)
		}
		lowered, err := preflightQuerySet(ctx, root.(*setProjectNode))
		if err != nil {
			t.Fatal(err)
		}
		live := lowered.(*setProjectNode)
		if len(live.calls) != 1 || live.callSlots[0] != 1 || !sameQuerySetOccurrence(live.calls[0], peer) {
			t.Fatal("strict erasure lost live peer or retained erased marker", live.calls, live.callSlots)
		}
	})
	fresh := seriesUnitType(t, sqltype.Int4)
	mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == sqltype.Int4 {
			return fresh
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(seriesExpression)
	if next.ResultUsage().Type != fresh || next.args[0].ResultUsage().Type != fresh || original.args[0].ResultUsage().Type != d || next.Properties() != original.Properties() || next.SourcePosition() != original.SourcePosition() {
		t.Fatal("retag mutation")
	}
	if got := seriesUnitRead(t, next); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatal(got)
	}
	walked := 0
	walkBoundExpression(original, func(n BoundExpr) {
		if _, ok := n.(literalExpression); ok {
			walked++
		}
	})
	if walked != 3 || plannedVolatility(original) != Immutable || querySetDepth(original) != 1 || !sameGroupExpression(original, original) {
		t.Fatal(walked)
	}
	if sameGroupExpression(original, seriesUnit(t, d, 1, 3, 2)) || sameGroupExpression(original, seriesUnit(t, fresh, 1, 3, 1)) {
		t.Fatal("step/width equality")
	}
	for _, fold := range []func() (BoundExpr, bool, error){
		func() (BoundExpr, bool, error) {
			return preflightCoercions(&EvalContext{Context: context.Background()}, original)
		},
		func() (BoundExpr, bool, error) {
			return simplifyConditional(&EvalContext{Context: context.Background()}, original, false)
		},
	} {
		n, _, err := fold()
		if err != nil || !isQuerySet(n) {
			t.Fatal(n, err)
		}
	}
	clone, err := remapBoundExpression(original, func(r ObjectRef) ObjectRef { return r }, nil)
	if err != nil || !sameGroupExpression(original, clone) {
		t.Fatal(clone, err)
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil || !strings.Contains(text, "generate_series(") || strings.Count(text, ",") != 2 {
		t.Fatal(text, err)
	}
	tree, err := (boundTree{expression: original}).StructuralOutput(context.Background())
	if err != nil || !strings.Contains(tree, "Series generate_series") || strings.Count(tree, "node Const") != 3 {
		t.Fatal(tree, err)
	}
	volatile := original
	volatile.args = append([]BoundExpr(nil), original.args...)
	probe := queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Integer(d, 1) }).(queryProbeExpression)
	probe.properties.Volatility = Volatile
	volatile.args[0] = probe
	if plannedVolatility(volatile) != Volatile {
		t.Fatal("erased volatile child")
	}
	root := &setProjectNode{querySchema: querySchema{{Usage: d.Usage()}}, child: &singletonNode{}, calls: []BoundExpr{original}}
	rewritten, err := rewriteQueryExpressions(root, func(n BoundExpr) (BoundExpr, bool, error) {
		return mapBoundExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, func(n BoundExpr) (BoundExpr, bool, error) {
			if n == original.args[0] {
				return seriesUnit(t, d, 2, 3).args[0], true, nil
			}
			return n, false, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := rewritten.(*setProjectNode).calls[0].(seriesExpression).args[0].(literalExpression).value.Integer()
	old, _ := original.args[0].(literalExpression).value.Integer()
	if n != 2 || old != 1 {
		t.Fatal("rewritten retained args", n, old)
	}
}

func seriesUnitProject(t *testing.T, view CatalogRead, expr seriesExpression) *projectNode {
	t.Helper()
	root, outputs, err := regexpSetUnitLower(view, &singletonNode{}, []BoundExpr{expr}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return &projectNode{querySchema: querySchema{{Name: "v", Usage: expr.ResultUsage()}}, child: root, expressions: outputs, frame: 1, hasTargetSet: true}
}
func TestSeriesLowering(t *testing.T) {
	view := New().CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	t.Run("bounded-demand", func(t *testing.T) {
		wide, _ := view.LookupType(sqltype.Int8)
		e := seriesUnit(t, wide, 1, math.MaxInt64)
		opens := 0
		q := seriesUnitProject(t, view, e)
		functionSetUnitArgument(t, q.child.(*setProjectNode).calls, 0, 0,
			queryProbe(wide.Usage(), func(*EvalContext) (sqltype.Value, error) { opens++; return sqltype.Integer(wide, 1) }))
		q.child = &limitNode{querySchema: q.child.Schema(), child: q.child, count: seriesUnit(t, wide, 3, 3).args[0]}
		r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
		if err != nil || count != 3 || opens != 1 {
			t.Fatal(count, opens, err)
		}
		defer r.Close()
		row := make([]sqltype.Value, 1)
		for _, want := range []int64{1, 2, 3} {
			ok, err := r.Next(context.Background(), row)
			n, _ := row[0].Integer()
			if err != nil || !ok || n != want {
				t.Fatal(n, ok, err)
			}
		}
		if ok, err := r.Next(context.Background(), row); ok || err != nil {
			t.Fatal(ok, err)
		}
	})
	inner := seriesUnit(t, d, 1, 3)
	outer := seriesUnit(t, d, 1, 3)
	outer.pos = parser.Position{Character: 5, Offset: 4}
	outer.args[1] = inner
	q := seriesUnitProject(t, view, outer)
	r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
	if err != nil || count != 6 {
		t.Fatal(count, err)
	}
	defer r.Close()
	var got []int64
	for {
		row := make([]sqltype.Value, 1)
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
	if !reflect.DeepEqual(got, []int64{1, 1, 2, 1, 2, 3}) {
		t.Fatal(got)
	}
}

func TestSeriesCancellation(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int8)
	cursor, err := openFunctionCall(&EvalContext{Context: context.Background()}, seriesUnit(t, d, 1, math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := cursor.next(context.Background(), d)
	if err != nil || !ok {
		t.Fatal(v, ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := cursor.next(ctx, d); ok || err != context.Canceled {
		t.Fatal(ok, err)
	}
	cursor.close()
	cursor.close()
	if _, ok, err := cursor.next(ctx, d); ok || err != context.Canceled || !cursor.exhausted {
		t.Fatal(ok, err)
	}
	r := newFinalRows(querySchema{{Name: "v", Usage: d.Usage()}}, 2)
	r.values = []sqltype.Value{v, v}
	row := make([]sqltype.Value, 1)
	if ok, err := r.Next(context.Background(), row); err != nil || !ok {
		t.Fatal(ok, err)
	}
	saved := row[0]
	schema := r.Schema()
	if ok, err := r.Next(ctx, row); ok || err != context.Canceled || r.values != nil {
		t.Fatal(ok, err)
	}
	if ok, err := r.Next(context.Background(), row); ok || err != context.Canceled {
		t.Fatal("nonsticky", ok, err)
	}
	r.Close()
	r.Close()
	if !reflect.DeepEqual(r.Schema(), schema) || saved.Type() != d {
		t.Fatal("cancel changed detached data")
	}
}

func TestSeriesFinalization(t *testing.T) {
	t.Run("later-invocation", func(t *testing.T) {
		view := New().CatalogRead()
		d, _ := view.LookupType(sqltype.Int4)
		e := seriesUnit(t, d, 1, 2)
		calls := 0
		sentinel := errors.New("later invocation child error")
		probe := queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls++
			if calls == 2 {
				return sqltype.Value{}, sentinel
			}
			return sqltype.Integer(d, 1)
		})
		input := &valuesNode{rows: [][]BoundExpr{{}, {}}}
		root, outputs, err := regexpSetUnitLower(view, input, []BoundExpr{e}, 1)
		if err != nil {
			t.Fatal(err)
		}
		functionSetUnitArgument(t, root.(*setProjectNode).calls, 0, 0, probe)
		q := &projectNode{querySchema: querySchema{{Usage: d.Usage()}}, child: root, expressions: outputs, frame: 1, hasTargetSet: true}
		r, count, err := finalizeQuery(context.Background(), q, &querySource{materialized: true}, nil)
		if r != nil || count != 0 || err != sentinel || calls != 2 {
			t.Fatal(r, count, err, calls)
		}
	})
	for _, phase := range []string{"error", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			view := New().CatalogRead()
			d, _ := view.LookupType(sqltype.Int4)
			e := seriesUnit(t, d, 1, 3)
			q := seriesUnitProject(t, view, e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			sentinel := errors.New("late projection error")
			q.expressions = []BoundExpr{queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) {
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
			source := &querySource{materialized: true}
			r, count, err := finalizeQuery(ctx, q, source, nil)
			want := sentinel
			if phase == "cancel" {
				want = context.Canceled
			}
			if r != nil || count != 0 || err != want || calls < 2 {
				t.Fatal(r, count, err, calls)
			}
		})
	}
}
