package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowExcludeMask(t *testing.T) {
	// Explicit admitted-member lists, independently specified for all four modes.
	for _, tc := range []struct {
		head, tail, current, first, last int
		want                             [4][]int
	}{
		{0, 3, 0, 0, 2, [4][]int{{0, 1, 2}, {1, 2}, {2}, {0, 2}}},
		{0, 3, 1, 0, 2, [4][]int{{0, 1, 2}, {0, 2}, {2}, {1, 2}}},
		{0, 1, 0, 0, 2, [4][]int{{0}, {}, {}, {0}}},
		{1, 3, 0, 0, 2, [4][]int{{1, 2}, {1, 2}, {2}, {2}}},
		{0, 1, 2, 1, 3, [4][]int{{0}, {0}, {0}, {0}}},
		{0, 3, 1, 0, 3, [4][]int{{0, 1, 2}, {0, 2}, {}, {1}}},
		{2, 2, 1, 0, 3, [4][]int{{}, {}, {}, {}}},
		{3, 1, 1, 0, 3, [4][]int{{}, {}, {}, {}}},
	} {
		for mode, want := range tc.want {
			got := []int{}
			for member := tc.head; member < tc.tail; member++ {
				if exec.WindowMaskAdmits(parser.FrameExclusion(mode), member, tc.current, tc.first, tc.last) {
					got = append(got, member)
				}
			}
			if !reflect.DeepEqual(got, want) || exec.WindowMaskCount(parser.FrameExclusion(mode), tc.head, tc.tail, tc.current, tc.first, tc.last) != len(want) {
				t.Fatal(tc, mode, got, want)
			}
		}
	}
}

func TestQueryWindowExcludeEndpoints(t *testing.T) {
	ctx := context.Background()
	peers := []int{2, 3}
	for row := range 3 {
		first, last, group := 0, 2, 0
		if row == 2 {
			first, last, group = 2, 3, 1
		}
		for _, unit := range []parser.FrameUnit{parser.FrameRows, parser.FrameGroups, parser.FrameRange} {
			var head, tail int
			var err error
			switch unit {
			case parser.FrameRows:
				head, err = exec.WindowRowsEndpoint(ctx, parser.FrameCurrentRow, 0, row, 3, false)
				if err == nil {
					tail, err = exec.WindowRowsEndpoint(ctx, parser.FrameCurrentRow, 0, row, 3, true)
				}
			case parser.FrameGroups:
				head, err = exec.WindowGroupsEndpoint(ctx, parser.FrameCurrentRow, 0, peers, group, false)
				if err == nil {
					tail, err = exec.WindowGroupsEndpoint(ctx, parser.FrameCurrentRow, 0, peers, group, true)
				}
			case parser.FrameRange:
				spec := plan.WindowSpec{}
				bound := plan.WindowBound{Kind: parser.FrameCurrentRow}
				head, err = exec.WindowFrameEndpoint(ctx, spec, bound, sqltype.Value{}, exec.WindowOrderState{}, []int{0, 1, 2}, first, last, 0, false, sqltype.InputEnvironment{})
				if err == nil {
					tail, err = exec.WindowFrameEndpoint(ctx, spec, bound, sqltype.Value{}, exec.WindowOrderState{}, []int{0, 1, 2}, first, last, 0, true, sqltype.InputEnvironment{})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			want := [4]int{last - first, last - first - 1, 0, 1}
			if unit == parser.FrameRows {
				want = [4]int{1, 0, 0, 1}
			}
			for mode, n := range want {
				if got := exec.WindowMaskCount(parser.FrameExclusion(mode), head, tail, row, first, last); got != n {
					t.Fatal(unit, row, mode, got, n)
				}
			}
		}
	}
}

func TestQueryWindowExcludeIdentityAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w,sum(v) OVER w,sum(v) OVER w,sum(v+1) OVER w,sum(v::bigint) OVER w,row_number() OVER w FROM t WINDOW w AS(ORDER BY id ROWS CURRENT ROW EXCLUDE GROUP)")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(node.Calls) != 6 || len(node.Specs) != 1 {
		t.Fatal(node)
	}
	for i, column := range query.Root.Schema() {
		if column.Nullable != (i >= 1 && i <= 4) || !column.NullabilityKnown || column.OriginTable != 0 {
			t.Fatal(i, column)
		}
	}
	if node.Calls[4].Expression.ResultUsage().Type.ID() != sqltype.Numeric {
		t.Fatal("bigint SUM result")
	}
	binder := &analyze.Binder{}
	a, b := node.Specs[0], node.Specs[0]
	b.Frame.Exclusion = parser.ExcludeTies
	if binder.SameWindowSpec(a, b) || !binder.SameWindowSort(a, b) || !binder.SameWindowCall(node.Calls[1], node.Calls[2]) {
		t.Fatal("semantic frame/call identity")
	}
	if node.Calls[1].Expression.Slot == node.Calls[2].Expression.Slot {
		t.Fatal("equal SUM targets shared execution ownership")
	}
	for _, other := range node.Calls[3:5] {
		if binder.SameWindowCall(node.Calls[1], other) {
			t.Fatal("distinct typed SUM arguments compared equal")
		}
	}
	left, right := node.Calls[1], node.Calls[2]
	left.Spec, right.Spec = a, b
	if binder.SameWindowCall(left, right) {
		t.Fatal("distinct exclusion modes compared equal")
	}
	left.Raw, right.Raw = &parser.WindowSpec{}, &parser.WindowSpec{}
	if !binder.SameWindowCall(left, right) {
		t.Fatal("identical unresolved SUM targets compared unequal")
	}
	for _, call := range node.Calls[1:5] {
		state, err := call.Provider.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		value, err := call.Provider.Finalize(context.Background(), state)
		if err != nil || !value.IsNull() || value.Type() != call.Expression.ResultUsage().Type {
			t.Fatal(value, err)
		}
	}
}

func TestQueryWindowExcludeGraphRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT sum(v+1) OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE TIES) FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	argument := node.Calls[0].Argument
	found := false
	plan.QueryExpressions(node, func(expr expr.Expr) { found = found || expr == argument })
	if !found {
		t.Fatal("argument absent from graph")
	}
	next, err := plan.RewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == argument {
			return node.Specs[0].Order[0].Expression, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*plan.WindowNode).Calls[0].Argument == argument || node.Calls[0].Argument != argument {
		t.Fatal("immutable argument rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := stored.RemapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	call := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	provider := call.Provider.(functions.IntegralSum)
	if call.Argument == argument || call.Argument.ResultUsage().Type != types[sqltype.Int4] || provider.InputType() != types[sqltype.Int4] || provider.ResultUsage().Type != types[sqltype.Int8] || call.Expression == node.Calls[0].Expression {
		t.Fatal("argument/provider ownership", call)
	}
}

type excludeCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	phase    string
	entered  bool
	computed bool
}

func (c *excludeCancelContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		window, hit, sum := false, false, false
		finalize, publication := false, false
		caller := ""
		for {
			frame, more := frames.Next()
			if caller == "" {
				caller = frame.Function
			}
			window = window || strings.HasSuffix(frame.Function, ".openWindowNode")
			sum = sum || strings.HasSuffix(frame.Function, ".(*WindowAggregates).Evaluate")
			hit = hit || strings.Contains(frame.Function, c.phase)
			finalize = finalize || strings.HasSuffix(frame.Function, ".IntegralSum.Finalize")
			publication = publication || strings.HasSuffix(frame.Function, "exec.Finalize")
			if !more {
				break
			}
		}
		c.computed = c.computed || window && finalize
		if c.phase == "sum-output" {
			hit = c.computed && strings.HasSuffix(caller, ".openWindowNode")
		}
		if c.phase == "sum-publication" {
			hit = c.computed && publication && !window
		}
		if c.phase == "ColumnExpr.Eval" {
			hit = hit && sum
		}
		if hit && (window || c.phase == "sum-publication") {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestQueryWindowExcludeCancellation(t *testing.T) {
	for _, exclusion := range []string{" EXCLUDE TIES", ""} {
		for _, phase := range []string{"CompareWindowRows", "WindowFrameCount", "IntegralSum.Init", "IntegralSum.Advance", "IntegralSum.Finalize", "ColumnExpr.Eval", "sum-output", "sum-publication"} {
			t.Run(phase+exclusion, func(t *testing.T) {
				_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w,sum(v) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING"+exclusion+")")
				continuation, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
				if err != nil {
					t.Fatal(err)
				}
				defer continuation.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				probe := &excludeCancelContext{Context: ctx, cancel: cancel, phase: phase}
				rows, n, err := exec.Finalize(probe, query.Root, source, nil)
				if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || n != 0 || source.View != nil {
					t.Fatal(probe.entered, rows, n, err)
				}
				source.Close()
				source.Close()
				recovered, n, err := exec.Finalize(context.Background(), query.Root, continuation, nil)
				if err != nil || recovered == nil || n != 4 {
					t.Fatal(recovered, n, err)
				}
				recovered.Close()
				recovered.Close()
			})
		}
	}
}

func TestQueryWindowExcludeFinishAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE s(id INT)")
	run(t, e, "INSERT INTO s VALUES(1),(2),(3)")
	run(t, e, "CREATE TABLE d(n BIGINT)")
	run(t, e, "INSERT INTO d VALUES(99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "INSERT INTO d SELECT sum(id) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) FROM s"), 0, nil)
	if err != nil || result.Count != 3 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !scope.Finished || scope.View != nil || scope.LockTicket != nil {
		t.Fatal("statement resources survived Finish")
	}
	current := run(t, e, "SELECT n FROM d").Rows
	defer current.Close()
	tuple := make([]sqltype.Value, 1)
	present, err := current.Next(context.Background(), tuple)
	value, _ := tuple[0].Integer()
	if !present || err != nil || value != 99 {
		t.Fatal(present, err, value)
	}
	if present, err := current.Next(context.Background(), tuple); present || err != nil {
		t.Fatal(present, err)
	}
	run(t, e, "INSERT INTO d SELECT sum(id) OVER(ROWS CURRENT ROW EXCLUDE TIES) FROM s")
}

func TestQueryWindowExcludePeers(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY v DESC NULLS FIRST,id) FROM t")
	defer source.Close()
	keys := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Specs[0].Order
	typ := keys[0].Expression.ResultUsage().Type
	one, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(typ, 2)
	if err != nil {
		t.Fatal(err)
	}
	values := []sqltype.Value{sqltype.Null(typ), one, sqltype.Null(typ), one, sqltype.Null(typ), two, two, one, one, one}
	for _, tc := range []struct{ a, b, want int }{
		{0, 1, 0}, {0, 2, -1}, {2, 0, 1}, {0, 3, -1}, {3, 4, -1}, {4, 3, 1},
	} {
		got, err := exec.CompareWindowRows(context.Background(), keys, values, tc.a, tc.b, sqltype.InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	got, err := exec.CompareWindowRows(context.Background(), nil, nil, 0, 4, sqltype.InputEnvironment{})
	if err != nil || got != 0 {
		t.Fatal("orderless partition must be peers", got, err)
	}
}

func TestQueryWindowExcludeLiteralCapture(t *testing.T) {
	sql := "SELECT sum(v+1) OVER(ROWS CURRENT ROW EXCLUDE TIES) FROM t"
	e, query, source := queryCoreFixture(t, sql)
	defer source.Close()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	binder := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	project := query.Root.(*plan.ProjectNode)
	window := project.Child.(*plan.WindowNode)
	binder.Grouping.WindowCalls = append([]plan.WindowCall(nil), window.Calls...)
	binder.Grouping.WindowCalls[0].Spec = window.Specs[0]
	raw := statement(t, sql).(*parser.Select).Targets[0]
	if err := binder.CaptureViewLiterals(raw, project.Expressions[0], analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	if len(binder.Capture.Literals) != 1 || len(binder.Capture.Columns) != 1 {
		t.Fatal("SUM argument children not captured", binder.Capture.Literals, binder.Capture.Columns)
	}
	for _, literal := range binder.Capture.Literals {
		value, err := literal.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		n, integerErr := value.Integer()
		if err != nil || integerErr != nil || n != 1 {
			t.Fatal(value, err, integerErr)
		}
	}
}

func TestQueryWindowExcludeGroupedArgument(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,sum(count(*)) OVER(ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM t GROUP BY v")
	defer source.Close()
	window := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if _, ok := window.Calls[0].Argument.(plan.GroupExpr); !ok {
		t.Fatal("grouped SUM argument lost aggregate slot", window.Calls[0].Argument)
	}
	rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || n != 3 {
		t.Fatal(rows, n, err)
	}
	defer rows.Close()
	tuple := make([]sqltype.Value, 2)
	for range 3 {
		present, err := rows.Next(context.Background(), tuple)
		// No window ORDER: TIES keeps each grouped occurrence's own COUNT.
		if !present || err != nil || tuple[1].IsNull() || tuple[1].Type().ID() != sqltype.Numeric {
			t.Fatal(present, tuple, err)
		}
	}
}

func TestQueryWindowExcludeDomainRemap(t *testing.T) {
	e := New()
	run(t, e, "CREATE DOMAIN exclude_arg AS INT")
	run(t, e, "CREATE TABLE t(id INT)")
	ast := statement(t, "SELECT sum(id::exclude_arg) OVER(ROWS CURRENT ROW EXCLUDE GROUP) FROM t")
	ref := roleTableRef(t, e, "t")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	description, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.Query.Root
	original := root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	var domain *sqltype.Descriptor
	plan.WalkExpression(original.Calls[0].Argument, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			domain = expr.ResultUsage().Type
		}
	})
	if domain == nil {
		t.Fatal("missing coerced SUM domain child")
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	types[domain.ID()], err = sqltype.Domain(domain.ID(), domain.DiagnosticName(), domain.SQLName(), types[domain.DomainBase().Type.ID()].Usage())
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] }
	mapped, err := stored.RemapViewQuery(New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	var mappedDomain *sqltype.Descriptor
	plan.WalkExpression(next.Calls[0].Argument, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			mappedDomain = expr.ResultUsage().Type
		}
	})
	if mappedDomain != types[domain.ID()] || mappedDomain == domain || !analyze.QueryDescriptorsChanged(root, lookup) || analyze.QueryDescriptorsChanged(mapped, lookup) || next.Specs[0].Frame.Exclusion != parser.ExcludeGroup {
		t.Fatal("SUM domain ownership", domain, mappedDomain)
	}
}
