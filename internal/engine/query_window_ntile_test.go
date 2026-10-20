package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowNtileState(t *testing.T) {
	for _, tc := range []struct {
		total, buckets int64
		want           []int64
	}{
		{6, 3, []int64{1, 1, 2, 2, 3, 3}},
		{7, 3, []int64{1, 1, 1, 2, 2, 3, 3}},
		{6, 4, []int64{1, 1, 2, 2, 3, 4}},
		{3, 5, []int64{1, 2, 3}},
		{3, math.MaxInt32, []int64{1, 2, 3}},
		{1, 1, []int64{1}},
	} {
		var state windowNtileState
		if err := state.initialize(tc.total, tc.buckets); err != nil {
			t.Fatal(err)
		}
		for i, want := range tc.want {
			if got := state.next(); got != want {
				t.Fatal(tc, i, got, want)
			}
		}
	}
	for _, buckets := range []int64{0, -1} {
		var state windowNtileState
		err := state.initialize(3, buckets)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22014" || diagnostic.Message != "argument of ntile must be greater than zero" || diagnostic.Position != "" || state.bucket != 0 {
			t.Fatal(state, err)
		}
	}
	// Conceptual partitions beyond INT4 size, without allocating their rows.
	var large windowNtileState
	if err := large.initialize(int64(math.MaxInt32)+2, 2); err != nil {
		t.Fatal(err)
	}
	if large.boundary != 1073741825 || large.remainder != 1 {
		t.Fatal(large)
	}
	large.members = 1073741824
	if large.next() != 1 || large.next() != 2 || large.boundary != 1073741824 || large.members != 1 {
		t.Fatal("large first/remainder transition", large)
	}
	if err := large.initialize(int64(math.MaxInt32)*2+1, math.MaxInt32); err != nil {
		t.Fatal(err)
	}
	if large.boundary != 3 || large.remainder != 1 {
		t.Fatal(large)
	}
	large.members = 3
	if large.next() != 2 || large.boundary != 2 {
		t.Fatal("large bucket count", large)
	}
	large.bucket = math.MaxInt32 - 1
	large.members = 2
	if large.next() != math.MaxInt32 {
		t.Fatal("last INT4 bucket", large)
	}
	if err := large.initialize(math.MaxInt64, 1); err != nil || large.boundary != math.MaxInt64 {
		t.Fatal("INT8 partition size", large, err)
	}
}

func TestQueryWindowNtileArgumentEvaluation(t *testing.T) {
	for _, prefix := range []int64{0, 1, 2} {
		_, query, source := queryCoreFixture(t, "SELECT ntile(2) OVER(ORDER BY id) FROM t")
		node := query.Root.(*projectNode).child.(*windowNode)
		call := &node.calls[0]
		argument := call.argument
		var evaluated []int64
		call.argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
			id, _ := c.Row[0].Integer()
			evaluated = append(evaluated, id)
			if id <= prefix {
				return sqltype.Null(argument.ResultUsage().Type), nil
			}
			return argument.Eval(c)
		})
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantEvaluated := []int64{1}
		if prefix == 1 {
			wantEvaluated = []int64{1, 2}
		}
		if prefix == 2 {
			wantEvaluated = []int64{1, 2, 3}
		}
		if count != 4 || !reflect.DeepEqual(evaluated, wantEvaluated) {
			rows.Close()
			t.Fatal("lazy current-row evaluation", prefix, count, evaluated)
		}
		want := [][]int64{{1, 1, 2, 2}, {0, 1, 1, 2}, {0, 0, 1, 1}}[prefix]
		values := make([]sqltype.Value, 1)
		for i, n := range want {
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil || values[0].IsNull() != (n == 0) {
				rows.Close()
				t.Fatal(prefix, i, present, values, err)
			}
			if n != 0 {
				got, err := values[0].Integer()
				if err != nil || got != n {
					rows.Close()
					t.Fatal(prefix, i, got, n, err)
				}
			}
		}
		if present, err := rows.Next(context.Background(), values); present || err != nil {
			rows.Close()
			t.Fatal("extra output", present, err)
		}
		rows.Close()
	}
	t.Run("partition-reset-and-separate-calls", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT ntile(1) OVER(PARTITION BY f ORDER BY id),ntile(2) OVER(PARTITION BY f ORDER BY id) FROM t")
		node := query.Root.(*projectNode).child.(*windowNode)
		var evaluated [2][]int64
		for i := range node.calls {
			original := node.calls[i].argument
			node.calls[i].argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				id, _ := c.Row[0].Integer()
				evaluated[i] = append(evaluated[i], id)
				return original.Eval(c)
			})
		}
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for _, ids := range evaluated {
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if !reflect.DeepEqual(ids, []int64{1, 2, 3}) {
				t.Fatal("partition/call state leaked", evaluated)
			}
		}
		if count != 4 {
			t.Fatal(count)
		}
		values := make([]sqltype.Value, 2)
		for i := range 4 {
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil {
				t.Fatal(present, err)
			}
			a, _ := values[0].Integer()
			b, _ := values[1].Integer()
			want := int64(1)
			if i == 3 {
				want = 2
			}
			if a != 1 || b != want {
				t.Fatal("separate bucket states", i, a, b)
			}
		}
	})
}

func TestQueryWindowNtileGraphAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT id,ntile(id) OVER w,ntile(id+1) OVER w,ntile((SELECT 2)) OVER w FROM t WINDOW w AS(ORDER BY id)")
	defer source.close()
	node := query.Root.(*projectNode).child.(*windowNode)
	for i, column := range query.Root.Schema() {
		if i == 0 {
			if column.OriginTable == 0 || column.OriginAttribute == 0 {
				t.Fatal("source origin lost", column)
			}
			continue
		}
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Type.ID() != sqltype.Int4 {
			t.Fatal("computed INT4/OID23", column)
		}
	}
	if len(node.calls) != 3 {
		t.Fatal(node.calls)
	}
	binder := &expressionBinder{}
	if binder.sameWindowCall(node.calls[0], node.calls[1]) || binder.sameWindowCall(node.calls[1], node.calls[2]) {
		t.Fatal("different bucket arguments collapsed")
	}
	call := node.calls[0]
	found := false
	queryExpressions(node, func(expr expr.Expr) { found = found || expr == call.argument })
	if !found {
		t.Fatal("bucket missing from graph")
	}
	replacement := node.calls[1].argument
	next, err := rewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == call.argument {
			return replacement, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*windowNode).calls[0].argument != replacement || node.calls[0].argument != call.argument {
		t.Fatal("immutable bucket rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*projectNode).child.(*windowNode).calls[0]
	if remapped.argument == call.argument || remapped.expression == call.expression || remapped.argument.ResultUsage().Type != types[sqltype.Int4] || remapped.expression.ResultUsage().Type != types[sqltype.Int4] {
		t.Fatal("bucket/result remap ownership", remapped)
	}
}

func TestQueryWindowNtileBindingUnwind(t *testing.T) {
	for _, tc := range []struct{ q, code string }{
		{"SELECT ntile(ntile(2) OVER(),1) OVER()", "42883"},
		{"SELECT ntile(ntile(2) OVER()) OVER()", "42P20"},
		{"SELECT ntile(ntile(2) OVER()) FILTER(WHERE TRUE) OVER()", "0A000"},
		{"SELECT ntile(missing) OVER()", "42703"},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause, b.query.frame = "SELECT", 1
		for _, q := range []string{"SELECT sum(1)", "SELECT row_number() OVER()"} {
			if _, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
		}
		before := b.grouping.windowCalls[:len(b.grouping.windowCalls):len(b.grouping.windowCalls)]
		b.grouping.windowCalls = before
		values := append([]windowCall(nil), before...)
		aggregates := b.grouping.aggregates[:len(b.grouping.aggregates):len(b.grouping.aggregates)]
		b.grouping.aggregates = aggregates
		_, err := b.bind(statement(t, tc.q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
		if len(b.grouping.windowCalls) != len(before) || cap(b.grouping.windowCalls) != cap(before) || &b.grouping.windowCalls[0] != &before[0] || !reflect.DeepEqual(values, b.grouping.windowCalls) || b.grouping.argumentOwner != (argumentWindowOwner{}) {
			t.Fatal("failed call changed window registration/owner", tc, b.grouping.windowCalls, b.grouping.argumentOwner)
		}
		if len(b.grouping.aggregates) != len(aggregates) || cap(b.grouping.aggregates) != cap(aggregates) || &b.grouping.aggregates[0] != &aggregates[0] {
			t.Fatal("failed call changed aggregate registration", tc)
		}
	}
}

// Cancel on the next output-loop check, after the post-argument check passed.
// This exercises cancellation after ntile has stopped evaluating its argument.
type ntileOutputCancellation struct {
	context.Context
	cancel context.CancelFunc
	armed  bool
	checks int
}

func (c *ntileOutputCancellation) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if c.armed {
		c.checks++
		if c.checks == 2 {
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestQueryWindowNtileEvaluationCancellation(t *testing.T) {
	for _, phase := range []string{"argument", "initialized-output"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT ntile(2) OVER(ORDER BY id) FROM t")
			node := query.Root.(*projectNode).child.(*windowNode)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &ntileOutputCancellation{Context: base, cancel: cancel}
			original := node.calls[0].argument
			entered := 0
			node.calls[0].argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered++
				if phase == "argument" {
					cancel()
				} else {
					ctx.armed = true
				}
				return value, err
			})
			rows, count, err := finalizeQuery(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var diagnostic *sqlstate.Error
			if entered != 1 || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || source.view != nil {
				t.Fatal("canceled ntile published/leaked", phase, entered, count, err, source.view)
			}
			if phase == "initialized-output" && ctx.checks != 2 {
				t.Fatal("did not reach initialized output loop", ctx.checks)
			}
		})
	}
}
