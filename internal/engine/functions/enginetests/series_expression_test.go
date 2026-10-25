package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func seriesUnit(t *testing.T, d *sqltype.Descriptor, values ...int64) plan.SeriesExpr {
	t.Helper()
	e := plan.SeriesExpr{Info: expr.Info{Usage: d.Usage(), Pos: parser.Position{Character: 8, Offset: 7}, Property: expr.Properties{StageDependent: true}}}
	for _, n := range values {
		v, err := sqltype.Integer(d, n)
		if err != nil {
			t.Fatal(err)
		}
		e.Args = append(e.Args, plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v})
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
func seriesUnitRead(t *testing.T, e plan.SeriesExpr) []int64 {
	t.Helper()
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	var out []int64
	for {
		v, ok, err := cursor.Next(context.Background())
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
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	lit := func(n int64) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
	}
	x, err := b.BindFunction(nullCountRaw("generate_series", lit(1), lit(3)))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := x.(plan.SeriesExpr)
	if !ok || e.ResultUsage().Type.ID() != sqltype.Int4 || !e.Properties().StageDependent || !b.Clause.QueryHasTargetSet || !exec.SourceAdmitted(e) {
		t.Fatal(x)
	}
	if _, err = e.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("scalar consumed set")
	}
	_, err = b.BindFunction(nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42725" {
		t.Fatal(err)
	}
	dateSeries, err := b.BindFunction(nullCountRaw("generate_series",
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "date"}, Text: "2020-01-01"},
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "date"}, Text: "2020-01-02"},
		parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "interval"}, Text: "1 day"}))
	if err != nil {
		t.Fatal(err)
	}
	if dateSeries.ResultUsage().Type.ID() != sqltype.Timestamptz || dateSeries.(plan.SeriesExpr).IntrinsicVolatility() != expr.Stable || !exec.SourceAdmitted(dateSeries) {
		t.Fatal("DATE category did not select executable preferred timestamptz", dateSeries)
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
	_, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "step size cannot equal zero" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	calls := 0
	e.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: sqltype.Null(d)}
	e.Args[1] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Integer(d, 3) })
	if got := seriesUnitRead(t, e); len(got) != 0 || calls != 1 {
		t.Fatal(got, calls)
	}
	sentinel := errors.New("sibling error")
	e.Args[1] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
	if _, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, e); err != sentinel {
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
	e := engine.New()
	read := e.CatalogRead()
	d, _ := read.LookupType(sqltype.Int4)
	original := seriesUnit(t, d, 1, 3, 1)
	t.Run("strict-arithmetic-constant-null", func(t *testing.T) {
		arithmetic, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, d, d, d)
		if err != nil {
			t.Fatal(err)
		}
		null := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: sqltype.Null(d)}
		wrapper := plan.ArithmeticExpr{Info: original.Info, Left: original, Right: null, Provider: arithmetic, Operator: sqltype.ArithmeticAdd}
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: read}}
		for _, fold := range []func() (expr.Expr, bool, error){
			func() (expr.Expr, bool, error) { return exec.PreflightCoercions(ctx, wrapper) },
			func() (expr.Expr, bool, error) { return exec.SimplifyConditional(ctx, wrapper, false) },
		} {
			node, changed, err := fold()
			value, ok := node.(plan.LiteralExpr)
			if err != nil || !changed || !ok || !value.Value.IsNull() || value.ResultUsage() != wrapper.ResultUsage() || exec.QuerySetDepth(node) != 0 {
				t.Fatal("strict set wrapper retained cardinality", node, changed, err)
			}
		}
		if exec.QuerySetDepth(wrapper) != 1 || exec.QuerySetDepth(original) != 1 {
			t.Fatal("mutated analyzed marker")
		}
		parameter := null
		parameter.Property.ParameterDependent = true
		retained := wrapper
		retained.Right = parameter
		if exec.ArithmeticSetConstantNull(retained, retained) {
			t.Fatal("imported parameter became a physical NULL proof")
		}
		peer := seriesUnit(t, d, 10, 11)
		peer.Pos = parser.Position{Offset: 20, Character: 21}
		root, _, err := regexpSetUnitLower(e, &plan.SingletonNode{}, []expr.Expr{wrapper, peer}, 1)
		if err != nil {
			t.Fatal(err)
		}
		lowered, err := view.PreflightQuery(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		live := lowered.(*plan.SetProjectNode)
		if len(live.Calls) != 1 || live.CallSlots[0] != 1 || !analyze.SameQuerySetOccurrence(live.Calls[0], peer) {
			t.Fatal("strict erasure lost live peer or retained erased marker", live.Calls, live.CallSlots)
		}
	})
	fresh := seriesUnitType(t, sqltype.Int4)
	mapped, changed, err := stored.RetagTypes(read, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == sqltype.Int4 {
			return fresh
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(plan.SeriesExpr)
	if next.ResultUsage().Type != fresh || next.Args[0].ResultUsage().Type != fresh || original.Args[0].ResultUsage().Type != d || next.Properties() != original.Properties() || next.SourcePosition() != original.SourcePosition() {
		t.Fatal("retag mutation")
	}
	if got := seriesUnitRead(t, next); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatal(got)
	}
	walked := 0
	plan.WalkExpression(original, func(n expr.Expr) {
		if _, ok := n.(plan.LiteralExpr); ok {
			walked++
		}
	})
	if walked != 3 || exec.PlannedVolatility(original) != expr.Immutable || exec.QuerySetDepth(original) != 1 || !analyze.SameGroupExpression(original, original) {
		t.Fatal(walked)
	}
	if analyze.SameGroupExpression(original, seriesUnit(t, d, 1, 3, 2)) || analyze.SameGroupExpression(original, seriesUnit(t, fresh, 1, 3, 1)) {
		t.Fatal("step/width equality")
	}
	for _, fold := range []func() (expr.Expr, bool, error){
		func() (expr.Expr, bool, error) {
			return exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original)
		},
		func() (expr.Expr, bool, error) {
			return exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original, false)
		},
	} {
		n, _, err := fold()
		if err != nil || !exec.SourceReturnsSet(n) {
			t.Fatal(n, err)
		}
	}
	clone, err := stored.RemapExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !analyze.SameGroupExpression(original, clone) {
		t.Fatal(clone, err)
	}
	text, err := (stored.Deparser{View: read}).SQL(context.Background(), original, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "generate_series(") || strings.Count(text, ",") != 2 {
		t.Fatal(text, err)
	}
	tree, err := (plan.BoundTree{Expression: original}).StructuralOutput(context.Background())
	if err != nil || !strings.Contains(tree, "Series generate_series") || strings.Count(tree, "node Const") != 3 {
		t.Fatal(tree, err)
	}
	volatile := original
	volatile.Args = append([]expr.Expr(nil), original.Args...)
	probe := queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Integer(d, 1) }).(queryProbeExpression)
	probe.Property.Volatility = expr.Volatile
	volatile.Args[0] = probe
	if exec.PlannedVolatility(volatile) != expr.Volatile {
		t.Fatal("erased volatile child")
	}
	root := &plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: d.Usage()}}, Child: &plan.SingletonNode{}, Calls: []expr.Expr{original}}
	rewritten, err := plan.RewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
		return stored.MapExpression(read, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(n expr.Expr) (expr.Expr, bool, error) {
			if n == original.Args[0] {
				return seriesUnit(t, d, 2, 3).Args[0], true, nil
			}
			return n, false, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := rewritten.(*plan.SetProjectNode).Calls[0].(plan.SeriesExpr).Args[0].(plan.LiteralExpr).Value.Integer()
	old, _ := original.Args[0].(plan.LiteralExpr).Value.Integer()
	if n != 2 || old != 1 {
		t.Fatal("rewritten retained args", n, old)
	}
}

func seriesUnitProject(t *testing.T, eng *engine.Engine, bound plan.SeriesExpr) *plan.ProjectNode {
	t.Helper()
	root, outputs, err := regexpSetUnitLower(eng, &plan.SingletonNode{}, []expr.Expr{bound}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: bound.ResultUsage()}}, Child: root, Expressions: outputs, Frame: 1, HasTargetSet: true}
}
func TestSeriesLowering(t *testing.T) {
	eng := engine.New()
	view := eng.CatalogRead()
	d, _ := view.LookupType(sqltype.Int4)
	t.Run("bounded-demand", func(t *testing.T) {
		wide, _ := view.LookupType(sqltype.Int8)
		e := seriesUnit(t, wide, 1, math.MaxInt64)
		opens := 0
		q := seriesUnitProject(t, eng, e)
		functionSetUnitArgument(t, q.Child.(*plan.SetProjectNode).Calls, 0, 0,
			queryProbe(wide.Usage(), func(*exec.Context) (sqltype.Value, error) { opens++; return sqltype.Integer(wide, 1) }))
		q.Child = &plan.LimitNode{QuerySchema: q.Child.Schema(), Child: q.Child, Count: seriesUnit(t, wide, 3, 3).Args[0]}
		r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
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
	outer.Pos = parser.Position{Character: 5, Offset: 4}
	outer.Args[1] = inner
	q := seriesUnitProject(t, eng, outer)
	r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
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
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, seriesUnit(t, d, 1, math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := cursor.Next(context.Background())
	if err != nil || !ok {
		t.Fatal(v, ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := cursor.Next(ctx); ok || err != context.Canceled {
		t.Fatal(ok, err)
	}
	cursor.Close()
	cursor.Close()
	if _, ok, err := cursor.Next(ctx); ok || err != context.Canceled || !cursor.Exhausted {
		t.Fatal(ok, err)
	}
	r := exec.NewFinalRows(plan.QuerySchema{{Name: "v", Usage: d.Usage()}}, 2)
	r.Values = []sqltype.Value{v, v}
	row := make([]sqltype.Value, 1)
	if ok, err := r.Next(context.Background(), row); err != nil || !ok {
		t.Fatal(ok, err)
	}
	saved := row[0]
	schema := r.Schema()
	if ok, err := r.Next(ctx, row); ok || err != context.Canceled || r.Values != nil {
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
		eng := engine.New()
		view := eng.CatalogRead()
		d, _ := view.LookupType(sqltype.Int4)
		e := seriesUnit(t, d, 1, 2)
		calls := 0
		sentinel := errors.New("later invocation child error")
		probe := queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) {
			calls++
			if calls == 2 {
				return sqltype.Value{}, sentinel
			}
			return sqltype.Integer(d, 1)
		})
		input := &plan.ValuesNode{Rows: [][]expr.Expr{{}, {}}}
		root, outputs, err := regexpSetUnitLower(eng, input, []expr.Expr{e}, 1)
		if err != nil {
			t.Fatal(err)
		}
		functionSetUnitArgument(t, root.(*plan.SetProjectNode).Calls, 0, 0, probe)
		q := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: d.Usage()}}, Child: root, Expressions: outputs, Frame: 1, HasTargetSet: true}
		r, count, err := exec.Finalize(context.Background(), q, &exec.Source{Materialized: true}, nil)
		if r != nil || count != 0 || err != sentinel || calls != 2 {
			t.Fatal(r, count, err, calls)
		}
	})
	for _, phase := range []string{"error", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			eng := engine.New()
			view := eng.CatalogRead()
			d, _ := view.LookupType(sqltype.Int4)
			e := seriesUnit(t, d, 1, 3)
			q := seriesUnitProject(t, eng, e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			sentinel := errors.New("late projection error")
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
			source := &exec.Source{Materialized: true}
			r, count, err := exec.Finalize(ctx, q, source, nil)
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

func regexpSetUnitLower(eng *engine.Engine, root plan.Node, expressions []expr.Expr, frame uint32) (plan.Node, []expr.Expr, error) {
	found := false
	for _, expression := range expressions {
		found = found || exec.QuerySetDepth(expression) != 0
	}
	if !found {
		return root, expressions, nil
	}
	outputs := append([]expr.Expr(nil), expressions...)
	b := &analyze.Binder{Host: eng.AnalysisHost(nil), Ctx: context.Background(), Query: analyze.QueryScopeState{Frame: frame}}
	root, err := b.LowerQuerySets(root, outputs, nil, false)
	return root, outputs, err
}

func functionSetUnitArgument(t *testing.T, calls []expr.Expr, callIndex, argumentIndex int, value expr.Expr) {
	t.Helper()
	args := append([]expr.Expr(nil), expr.Expr(calls[callIndex]).(expr.Composite).Children()...)
	args[argumentIndex] = value
	next, err := expr.Substitute(expr.Expr(calls[callIndex]).(expr.Composite), expr.InfoOf(calls[callIndex]), args)
	if err != nil {
		t.Fatal(err)
	}
	calls[callIndex] = next
}
