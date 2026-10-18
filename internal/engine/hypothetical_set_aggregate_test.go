package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func hypotheticalUnitInput(t *testing.T, id sqltype.ID, direct string, values ...string) (hypotheticalSetProvider, orderedAggregateInput) {
	t.Helper()
	catalog := New().CatalogRead()
	input, _ := catalog.LookupType(id)
	result, _ := catalog.LookupType(sqltype.Int8)
	comparison, err := sqltype.BindOrdering(input.Usage())
	if err != nil {
		t.Fatal(err)
	}
	in := orderedAggregateInput{width: 3, values: make([]sqltype.Value, 3*len(values)), indices: make([]int, len(values)), target: make([]sqltype.Value, 3), order: []sortKey{{comparison: comparison, cache: 2, slot: -1}}}
	parse := func(text string) sqltype.Value {
		if text == "NULL" {
			return sqltype.Null(input)
		}
		v, err := input.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	in.target[2] = parse(direct)
	for i, text := range values {
		in.values[3*i+2] = parse(text)
		in.indices[i] = i
	}
	return hypotheticalSetProvider{usage: result.Usage(), operation: aggregateHypotheticalRank}, in
}

func TestHypotheticalAggregateTupleEquality(t *testing.T) {
	for _, tc := range []struct {
		id     sqltype.ID
		direct string
		values []string
	}{
		{sqltype.Numeric, "1.00", []string{"1", "1.0", "1.00"}},
		{sqltype.Float8, "NaN", []string{"NaN", "NaN"}},
		{sqltype.Float8, "-0", []string{"0", "-0"}},
		{sqltype.BPChar, "a", []string{"a", "a ", "a  "}},
	} {
		_, in := hypotheticalUnitInput(t, tc.id, tc.direct, tc.values...)
		for i := range in.len() {
			sign, err := compareOrderedAggregateTuple(context.Background(), in.order, in.tuple(i), in.target, sqltype.InputEnvironment{})
			if err != nil || sign != 0 {
				t.Fatal(tc, sign, err)
			}
		}
	}
	_, in := hypotheticalUnitInput(t, sqltype.Int4, "2", "1", "2", "NULL")
	for _, desc := range []bool{false, true} {
		for _, nullsFirst := range []bool{false, true} {
			in.order[0].desc, in.order[0].nullsFirst = desc, nullsFirst
			a, err := compareOrderedAggregateTuple(context.Background(), in.order, in.tuple(0), in.target, sqltype.InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			b, err := compareOrderedAggregateTuple(context.Background(), in.order, in.target, in.tuple(0), sqltype.InputEnvironment{})
			if err != nil || a != -b || (a < 0) == desc {
				t.Fatal("direction/antisymmetry", a, b, err)
			}
			a, err = compareOrderedAggregateTuple(context.Background(), in.order, in.tuple(2), in.target, sqltype.InputEnvironment{})
			if err != nil || (a < 0) != nullsFirst {
				t.Fatal("NULL direction", a, err)
			}
		}
	}
}

func TestHypotheticalAggregateFormulasAndBorrowing(t *testing.T) {
	for _, tc := range []struct {
		direct string
		want   []string
	}{
		{"5", []string{"1", "1", "0", "0.2"}}, {"20", []string{"2", "2", "0.25", "0.8"}},
		{"30", []string{"4", "3", "0.75", "0.8"}}, {"50", []string{"5", "4", "1", "1"}},
	} {
		p, in := hypotheticalUnitInput(t, sqltype.Int4, tc.direct, "10", "20", "20", "40")
		before := append([]sqltype.Value(nil), in.values...)
		target := append([]sqltype.Value(nil), in.target...)
		for i, op := range []aggregateOperation{aggregateHypotheticalRank, aggregateHypotheticalDenseRank, aggregateHypotheticalPercentRank, aggregateHypotheticalCumeDist} {
			p.operation = op
			id := sqltype.Int8
			if i >= 2 {
				id = sqltype.Float8
			}
			p.usage.Type, _ = New().CatalogRead().LookupType(id)
			out, err := p.FinalizeOrdered(context.Background(), orderedSetPreparation{}, in, sqltype.InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			text, err := p.usage.Type.Output(context.Background(), out)
			if err != nil || text != tc.want[i] {
				t.Fatal(tc, op, text, err)
			}
			if !reflect.DeepEqual(before, in.values) || !reflect.DeepEqual(target, in.target) {
				t.Fatal("borrowed tuples mutated")
			}
			empty := in
			empty.indices = nil
			empty.values = nil
			out, err = p.FinalizeOrdered(context.Background(), orderedSetPreparation{}, empty, sqltype.InputEnvironment{})
			if err != nil || out.IsNull() {
				t.Fatal("empty nonnull", err)
			}
			text, err = p.usage.Type.Output(context.Background(), out)
			want := "1"
			if i == 2 {
				want = "0"
			}
			if err != nil || text != want {
				t.Fatal("empty formula", op, text, err)
			}
		}
	}
}

func TestHypotheticalAggregateFiftyKeyExecution(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   sqltype.ID
		want string
	}{
		{"rank", sqltype.Int8, "1"},
		{"dense_rank", sqltype.Int8, "1"},
		{"percent_rank", sqltype.Float8, "0"},
		{"cume_dist", sqltype.Float8, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			b.aggregateClause = "SELECT"
			position := parser.Position{Character: 8}
			raw := parser.AggregateExpr{Name: tc.name, Within: true, Pos: position}
			for i := range 50 {
				expr := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(i + 1), Pos: position}}
				raw.Args = append(raw.Args, expr)
				raw.Order = append(raw.Order, parser.OrderKey{Expr: expr, Pos: position})
			}
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			call := b.aggregates[node.(aggregateExpression).slot]
			if len(call.direct) != 50 || len(call.order) != 50 || call.stageWidth != 52 || call.argument != nil || !call.includeNulls {
				t.Fatal("full tuple layout", call)
			}
			for i, key := range call.order {
				if key.slot != -1 || key.cache != i+2 {
					t.Fatal("independent key cache", i, key)
				}
			}
			if node.ResultUsage() != b.typ(tc.id).Usage() || node.ResultUsage().Collation.ID != 0 {
				t.Fatal("result authority", node.ResultUsage())
			}
			child := &valuesNode{rows: [][]BoundExpr{{}}}
			aggregate := &aggregateNode{querySchema: querySchema{{Usage: node.ResultUsage()}}, child: child, calls: []aggregateCall{call}}
			root := &projectNode{querySchema: aggregate.querySchema, child: aggregate, expressions: []BoundExpr{groupExpression{expressionInfo: expressionInfo{Usage: node.ResultUsage()}, slot: 0, frame: 1}}, frame: 1}
			rows, count, err := finalizeQuery(context.Background(), root, &querySource{}, nil)
			if err != nil || count != 1 {
				t.Fatal("50-key private execution", count, err)
			}
			defer rows.Close()
			out := make([]sqltype.Value, 1)
			ok, err := rows.Next(context.Background(), out)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			if out[0].Type() != b.typ(tc.id) || out[0].IsNull() {
				t.Fatal("50-key result type", out[0].Type())
			}
			value, err := out[0].Type().Output(context.Background(), out[0])
			if err != nil || value != tc.want {
				t.Fatal("50-key result", value, tc.want, err)
			}
			other := call
			other.direct = append([]BoundExpr(nil), call.direct...)
			other.direct[49] = call.direct[0]
			if b.sameAggregateCall(call, other) {
				t.Fatal("later direct identity omitted")
			}
			// Copy-on-change must not alter the original direct slice.
			rewritten, err := rewriteQueryExpressions(aggregate, func(expr BoundExpr) (BoundExpr, bool, error) {
				if b.sameGroupExpression(expr, call.direct[49]) {
					return call.direct[0], true, nil
				}
				return expr, false, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !b.sameGroupExpression(rewritten.(*aggregateNode).calls[0].direct[49], call.direct[0]) || !b.sameGroupExpression(aggregate.calls[0].direct[49], call.direct[49]) {
				t.Fatal("rewrite aliased direct slice")
			}
			// Every direct/key/result descriptor must belong to the destination catalog.
			destination := New().CatalogRead()
			remapped, err := remapViewQuery(destination, aggregate, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil {
					return nil
				}
				next, ok := destination.LookupType(old.ID())
				if !ok {
					t.Fatal("destination type", old.ID())
				}
				return next
			})
			if err != nil {
				t.Fatal(err)
			}
			mapped := remapped.(*aggregateNode).calls[0]
			expressions := append([]BoundExpr(nil), mapped.direct...)
			for _, key := range mapped.order {
				expressions = append(expressions, key.expression)
			}
			for _, expr := range expressions {
				want, _ := destination.LookupType(expr.ResultUsage().Type.ID())
				if expr.ResultUsage().Type != want {
					t.Fatal("source operand descriptor retained")
				}
			}
			want, _ := destination.LookupType(tc.id)
			if mapped.provider.ResultUsage().Type != want {
				t.Fatal("source result descriptor retained")
			}
		})
	}
}

// Observe typed Compare entries, not the number of unrelated executor checks.
// Integer comparison calls Err exactly once, so the third provider Compare is
// the first dense peer comparison after two placement comparisons.
type hypotheticalCompareCancelContext struct {
	context.Context
	cancel       context.CancelFunc
	at, seen     int
	providerOnly bool
}

func (c *hypotheticalCompareCancelContext) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	comparison, provider := false, false
	for {
		frame, more := frames.Next()
		comparison = comparison || strings.HasSuffix(frame.Function, ".Comparison.Compare")
		provider = provider || strings.HasSuffix(frame.Function, ".(*hypotheticalSetProvider).FinalizeOrdered")
		if !more {
			break
		}
	}
	if comparison && (!c.providerOnly || provider) {
		c.seen++
		if c.seen == c.at {
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestHypotheticalAggregateCancellationPublication(t *testing.T) {
	for _, phase := range []string{"success", "direct_first", "direct_later", "sort_compare", "placement", "dense_peer", "prepare", "finalize", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			p, in := hypotheticalUnitInput(t, sqltype.Int4, "30", "10", "10", "20")
			p.operation = aggregateHypotheticalDenseRank
			input := in.target[2].Type()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var execution context.Context = ctx
			var family *aggregateModifierFamilyCancelContext
			var compare *hypotheticalCompareCancelContext
			switch phase {
			case "sort_compare", "placement", "dense_peer":
				at := 1
				if phase == "dense_peer" {
					at = 3
				}
				compare = &hypotheticalCompareCancelContext{Context: ctx, cancel: cancel, at: at, providerOnly: phase != "sort_compare"}
				execution = compare
			case "prepare", "finalize":
				target := ".(*hypotheticalSetProvider).PrepareOrdered"
				if phase == "finalize" {
					target = ".(*hypotheticalSetProvider).FinalizeOrdered"
				}
				family = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = family
			}
			directCalls, keyCalls := []int{}, 0
			var direct []BoundExpr
			for i := range 2 {
				direct = append(direct, queryProbe(input.Usage(), func(*EvalContext) (sqltype.Value, error) {
					directCalls = append(directCalls, i)
					if phase == "direct_first" && i == 0 || phase == "direct_later" && i == 1 {
						cancel()
					}
					return in.target[2], nil
				}))
			}
			key := queryProbe(input.Usage(), func(c *EvalContext) (sqltype.Value, error) { keyCalls++; return c.Row[0], nil })
			call := aggregateCall{provider: &p, direct: direct, operation: p.operation, stageWidth: 4, includeNulls: true, order: []sortKey{{expression: key, comparison: in.order[0].comparison, slot: -1, cache: 2}, {expression: key, comparison: in.order[0].comparison, slot: -1, cache: 3}}}
			child := &valuesNode{querySchema: querySchema{{Usage: input.Usage()}}}
			for i := range in.len() {
				child.rows = append(child.rows, []BoundExpr{literalExpression{expressionInfo: expressionInfo{Usage: input.Usage()}, value: in.tuple(i)[2]}})
			}
			aggregate := &aggregateNode{querySchema: querySchema{{Usage: p.usage}}, child: child, calls: []aggregateCall{call}}
			root := &projectNode{querySchema: aggregate.querySchema, child: aggregate, expressions: []BoundExpr{groupExpression{expressionInfo: expressionInfo{Usage: p.usage}, slot: 0, frame: 1}}, frame: 1}
			if phase == "prepublication" {
				result := root.expressions[0]
				root.expressions[0] = queryProbe(p.usage, func(c *EvalContext) (sqltype.Value, error) { value, err := result.Eval(c); cancel(); return value, err })
			}
			owned := &querySource{anonymous: [][]sqltype.Value{{sqltype.Null(input)}}, flat: []sqltype.Value{sqltype.Null(input)}}
			source := &querySource{sources: []*querySource{owned}}
			rows, count, err := finalizeQuery(execution, root, source, nil)
			if phase == "success" {
				if err != nil || count != 1 || !reflect.DeepEqual(directCalls, []int{0, 1}) || keyCalls != 6 {
					t.Fatal("evaluation frequencies/order", directCalls, keyCalls, count, err)
				}
				rows.Close()
				return
			}
			if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.sources != nil || owned.flat != nil {
				t.Fatal("unpublished failure/resource release", phase, rows, count, err)
			}
			if compare != nil && compare.seen < compare.at {
				t.Fatal("comparison phase not reached", compare.seen)
			}
			if family != nil && !family.entered {
				t.Fatal("family phase not reached", phase)
			}
		})
	}
}
