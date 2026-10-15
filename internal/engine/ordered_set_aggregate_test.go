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

func orderedSetUnitProvider(t *testing.T, operation aggregateOperation, id sqltype.ID, array bool) orderedSetProvider {
	t.Helper()
	catalog := New().CatalogRead()
	input, ok := catalog.LookupType(id)
	if !ok {
		t.Fatal("input descriptor")
	}
	fraction, _ := catalog.LookupType(sqltype.Float8)
	usage := input.Usage()
	comparison, err := sqltype.BindOrdering(usage)
	if err != nil {
		t.Fatal(err)
	}
	if array {
		result, ok := catalog.LookupArray(input)
		if !ok || !result.Concrete() {
			t.Fatal("array descriptor")
		}
		usage.Type = result
	}
	p := orderedSetProvider{usage: usage, input: input, fraction: fraction, operation: operation, array: array, comparison: comparison}
	if operation == aggregatePercentileCont && id == sqltype.Interval {
		p.subtract, err = sqltype.BindArithmetic(sqltype.ArithmeticSubtract, input, input, input)
		if err != nil {
			t.Fatal(err)
		}
		p.multiply, err = sqltype.BindArithmetic(sqltype.ArithmeticMultiply, input, fraction, input)
		if err != nil {
			t.Fatal(err)
		}
		p.add, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, input, input, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func orderedSetUnitInput(t *testing.T, p orderedSetProvider, texts ...string) orderedAggregateInput {
	t.Helper()
	in := orderedAggregateInput{width: 2, indices: make([]int, len(texts)), values: make([]sqltype.Value, len(texts)*2)}
	for i, text := range texts {
		value, err := p.input.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		in.indices[i] = i
		in.values[i*2] = value
	}
	return in
}

func orderedSetUnitFractionArray(t *testing.T, p orderedSetProvider) *sqltype.Descriptor {
	t.Helper()
	// Pair the executable codec with this provider's exact leaf generation.
	array, err := sqltype.Array(p.fraction.AssociatedArray(), "_float8", p.fraction)
	if err != nil {
		t.Fatal(err)
	}
	return array
}

func orderedSetUnitFinalize(ctx context.Context, p *orderedSetProvider, direct sqltype.Value, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	prepared, err := p.PrepareOrdered(ctx, []sqltype.Value{direct}, in.len())
	if err != nil {
		return sqltype.Value{}, err
	}
	if prepared.terminal {
		return prepared.value, ctx.Err()
	}
	return p.FinalizeOrdered(ctx, prepared, in, env)
}

func TestOrderedSetAggregateSevenProviders(t *testing.T) {
	for _, tc := range []struct {
		operation aggregateOperation
		id        sqltype.ID
		array     bool
		want      string
	}{
		{aggregatePercentileCont, sqltype.Float8, false, "25"}, {aggregatePercentileCont, sqltype.Interval, false, "25 days"},
		{aggregatePercentileCont, sqltype.Float8, true, "{25}"}, {aggregatePercentileCont, sqltype.Interval, true, "{\"25 days\"}"},
		{aggregatePercentileDisc, sqltype.Int4, false, "20"}, {aggregatePercentileDisc, sqltype.Int4, true, "{20}"}, {aggregateMode, sqltype.Int4, false, "10"},
	} {
		p := orderedSetUnitProvider(t, tc.operation, tc.id, tc.array)
		texts := []string{"10", "20", "30", "40"}
		if tc.id == sqltype.Interval {
			texts = []string{"10 days", "20 days", "30 days", "40 days"}
		}
		in := orderedSetUnitInput(t, p, texts...)
		before := append([]sqltype.Value(nil), in.values...)
		direct, _ := sqltype.Float(p.fraction, .5)
		if tc.array {
			var err error
			direct, err = orderedSetUnitFractionArray(t, p).Input(context.Background(), "{.5}", -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		value, err := orderedSetUnitFinalize(context.Background(), &p, direct, in, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := p.usage.Type.Output(context.Background(), value)
		if err != nil || text != tc.want {
			t.Fatalf("%v %q want %q: %v", tc, text, tc.want, err)
		}
		if !reflect.DeepEqual(before, in.values) {
			t.Fatal("borrowed payload mutated")
		}
	}
}

func TestOrderedSetAggregateShapeAndEmptyPrecedence(t *testing.T) {
	p := orderedSetUnitProvider(t, aggregatePercentileCont, sqltype.Float8, true)
	fractionArray := orderedSetUnitFractionArray(t, p)
	direct, err := fractionArray.Input(context.Background(), "[-1:0][3:5]={{1,NULL,0},{.5,.25,.5}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	in := orderedSetUnitInput(t, p, "10", "20", "30", "40")
	out, err := orderedSetUnitFinalize(context.Background(), &p, direct, in, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if out.ArrayRank() != 2 || out.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -1, Length: 2}) || out.ArrayDimension(1) != (sqltype.ArrayDimension{Lower: 3, Length: 3}) {
		t.Fatal("lost shape")
	}
	for i, want := range []float64{40, 0, 10, 25, 17.5, 25} {
		if i == 1 {
			if !out.ArrayElement(i).IsNull() {
				t.Fatal("lost NULL")
			}
			continue
		}
		got, err := out.ArrayElement(i).Float()
		if err != nil || got != want {
			t.Fatal(i, got, want, err)
		}
	}
	invalid, _ := fractionArray.Input(context.Background(), "{1.1,-.1}", -1)
	empty, err := orderedSetUnitFinalize(context.Background(), &p, invalid, orderedAggregateInput{}, sqltype.InputEnvironment{})
	if err != nil || !empty.IsNull() {
		t.Fatal("array validated empty state", err)
	}
	_, err = orderedSetUnitFinalize(context.Background(), &p, invalid, in, sqltype.InputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22003" || d.Message != "percentile value 1.1 is not between 0 and 1" || d.Position != "" {
		t.Fatal(err)
	}
	p.array = false
	p.usage = p.input.Usage()
	bad, _ := sqltype.Float(p.fraction, 1.1)
	_, err = orderedSetUnitFinalize(context.Background(), &p, bad, orderedAggregateInput{}, sqltype.InputEnvironment{})
	if !errors.As(err, &d) || d.Code != "22003" {
		t.Fatal("scalar skipped validation", err)
	}
}

func TestOrderedSetAggregateIEEEAndModeEquality(t *testing.T) {
	p := orderedSetUnitProvider(t, aggregatePercentileCont, sqltype.Float8, false)
	in := orderedSetUnitInput(t, p, "Infinity", "Infinity")
	for _, fraction := range []float64{0, .5, 1} {
		direct, _ := sqltype.Float(p.fraction, fraction)
		out, err := orderedSetUnitFinalize(context.Background(), &p, direct, in, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := out.Float()
		if fraction == .5 && !math.IsNaN(got) || fraction != .5 && !math.IsInf(got, 1) {
			t.Fatal(fraction, got)
		}
	}
	for _, tc := range []struct {
		id     sqltype.ID
		values []string
		want   string
	}{
		{sqltype.Numeric, []string{"1", "1.00", "2"}, "1"},
		{sqltype.Float8, []string{"1", "NaN", "NaN"}, "NaN"},
		{sqltype.Inet, []string{"10.0.0.1", "10.0.0.1", "10.0.0.2"}, "10.0.0.1"},
	} {
		mode := orderedSetUnitProvider(t, aggregateMode, tc.id, false)
		out, err := orderedSetUnitFinalize(context.Background(), &mode, sqltype.Value{}, orderedSetUnitInput(t, mode, tc.values...), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		want, err := mode.input.Input(context.Background(), tc.want, -1)
		if err != nil {
			t.Fatal(err)
		}
		cmp, err := mode.comparison.Compare(context.Background(), out, want, sqltype.InputEnvironment{})
		if err != nil || cmp != 0 {
			t.Fatal(tc, cmp, err)
		}
	}
}

func TestOrderedSetAggregateCancellationPhases(t *testing.T) {
	for _, phase := range []string{".sortOrderedAggregate.func1", ".(*orderedSetProvider).mode", ".(*orderedSetProvider).PrepareOrdered", ".(*orderedSetProvider).FinalizeOrdered", ".(*orderedSetProvider).percentile"} {
		t.Run(phase, func(t *testing.T) {
			operation := aggregatePercentileCont
			if phase == ".(*orderedSetProvider).mode" {
				operation = aggregateMode
			}
			p := orderedSetUnitProvider(t, operation, sqltype.Float8, false)
			in := orderedSetUnitInput(t, p, "10", "20", "30", "40")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: phase}
			direct, _ := sqltype.Float(p.fraction, .5)
			var err error
			if phase == ".sortOrderedAggregate.func1" {
				_, err = sortOrderedAggregate(probe, &queryState{}, aggregateCall{stageWidth: 2, order: []sortKey{{comparison: p.comparison, cache: 0}}}, in.values)
			} else {
				_, err = orderedSetUnitFinalize(probe, &p, direct, in, sqltype.InputEnvironment{})
			}
			if !probe.entered || !errors.Is(err, context.Canceled) {
				t.Fatal(phase, probe.entered, err)
			}
		})
	}
}

func TestOrderedSetAggregateGroupFrameAndPublication(t *testing.T) {
	p := orderedSetUnitProvider(t, aggregatePercentileCont, sqltype.Float8, false)
	info := expressionInfo{usage: p.input.Usage(), properties: ExprProperties{RowDependent: true}}
	key := columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 0}
	arg := columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 1}
	grouping, err := sqltype.BindGrouping(key.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	literal := func(n float64) BoundExpr {
		value, _ := sqltype.Float(p.input, n)
		return literalExpression{expressionInfo: expressionInfo{usage: p.input.Usage()}, value: value}
	}
	for _, phase := range []string{"success", "having", "direct", "prepare", "sort", "scan", "array", "percentile"} {
		t.Run(phase, func(t *testing.T) {
			provider := p
			if phase == "scan" {
				provider.operation = aggregateMode
			}
			if phase == "array" || phase == "prepare" {
				provider.usage.Type = orderedSetUnitFractionArray(t, provider)
				provider.array = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var execution context.Context = ctx
			target := ""
			switch phase {
			case "prepare":
				target = ".(*orderedSetProvider).PrepareOrdered"
			case "sort":
				target = ".sortOrderedAggregate.func1"
			case "scan":
				target = ".(*orderedSetProvider).mode"
			case "array":
				target = ".(*orderedSetProvider).arrayPercentiles"
			case "percentile":
				target = ".(*orderedSetProvider).percentile"
			}
			var probe *aggregateModifierFamilyCancelContext
			if target != "" {
				probe = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = probe
			}
			calls := 0
			direct := queryProbe(provider.fraction.Usage(), func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if phase == "direct" {
					cancel()
				}
				if provider.array {
					return orderedSetUnitFractionArray(t, provider).Input(c.Context, "{.25,.75}", -1)
				}
				return groupExpression{expressionInfo: expressionInfo{usage: provider.fraction.Usage()}, slot: 0, frame: 1}.Eval(c)
			})
			child := &valuesNode{querySchema: querySchema{{Usage: p.input.Usage()}, {Usage: p.input.Usage()}}, rows: [][]BoundExpr{{literal(.25), literal(10)}, {literal(.25), literal(20)}, {literal(.75), literal(30)}, {literal(.75), literal(40)}}}
			call := aggregateCall{provider: &provider, argument: arg, direct: []BoundExpr{direct}, operation: provider.operation, stageWidth: 2, order: []sortKey{{expression: arg, comparison: provider.comparison, slot: 0, cache: 0}}}
			aggregate := &aggregateNode{querySchema: querySchema{{Usage: p.input.Usage()}, {Usage: provider.usage}}, child: child, keys: []BoundExpr{key}, grouping: []sqltype.Grouping{grouping}, calls: []aggregateCall{call}}
			root := &projectNode{querySchema: querySchema{{Usage: provider.usage}}, child: aggregate, expressions: []BoundExpr{groupExpression{expressionInfo: expressionInfo{usage: provider.usage}, slot: 1, frame: 1}}, frame: 1}
			if phase == "having" {
				boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
				root.child = &filterNode{querySchema: aggregate.querySchema, child: aggregate, predicate: literalExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}}
			}
			owned := &querySource{anonymous: [][]sqltype.Value{{sqltype.Null(p.input)}}, flat: []sqltype.Value{sqltype.Null(p.input)}}
			source := &querySource{sources: []*querySource{owned}}
			rows, count, err := finalizeQuery(execution, root, source, nil)
			if phase == "having" {
				if err != nil || rows == nil || count != 0 || calls != 2 {
					t.Fatal("HAVING bypassed ordered-set finalization", calls, count, err)
				}
				rows.Close()
				return
			}
			if phase != "success" {
				if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.sources != nil || owned.flat != nil {
					t.Fatal("publication/source ownership", phase, rows, count, err)
				}
				if probe != nil && !probe.entered {
					t.Fatal("actual phase not entered", phase)
				}
				return
			}
			if err != nil || count != 2 || calls != 2 {
				t.Fatal("direct group frequency", calls, count, err)
			}
			defer rows.Close()
			out := make([]sqltype.Value, 1)
			values := make([]float64, 0, 2)
			for range 2 {
				present, err := rows.Next(context.Background(), out)
				if err != nil || !present {
					t.Fatal(present, err)
				}
				value, _ := out[0].Float()
				values = append(values, value)
			}
			if !reflect.DeepEqual(values, []float64{12.5, 37.5}) {
				t.Fatal("wrong group frame", values)
			}
		})
	}
}

func TestOrderedSetAggregateResolverAndIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	position := parser.Position{Character: 8}
	for _, tc := range []struct {
		name, input, result string
		array               bool
	}{
		{"percentile_cont", "float8", "double precision", false},
		{"percentile_cont", "interval", "interval", false},
		{"percentile_cont", "float8", "double precision[]", true},
		{"percentile_cont", "interval", "interval[]", true},
		{"percentile_disc", "int4", "integer", false},
		{"percentile_disc", "int4", "integer[]", true},
		{"mode", "int4", "integer", false},
	} {
		input := "1"
		if tc.input == "interval" {
			input = "1 day"
		}
		raw := parser.AggregateExpr{Name: tc.name, Within: true, Pos: position, Order: []parser.OrderKey{{Expr: parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: tc.input}, Text: input, Pos: position}, Pos: position}}}
		if tc.name != "mode" {
			if tc.array {
				raw.Args = []parser.Expr{parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "float8", ArrayBounds: []int32{0}}, Text: "{.5}", Pos: position}}
			} else {
				raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NumericValue, Text: ".5", Pos: position}}}
			}
		}
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name, Pos: position}}}
		node, err := b.bindOrderedSet(raw, name)
		if err != nil {
			t.Fatal(tc, err)
		}
		if got := node.ResultUsage().Type.DiagnosticName(); got != tc.result {
			t.Fatal(tc, got)
		}
		call := b.aggregates[node.(aggregateExpression).slot]
		if call.order[0].slot != 0 || call.order[0].cache != 0 || call.stageWidth != 2 || (call.direct == nil) != (tc.name == "mode") {
			t.Fatal("uncached input/direct identity", tc, call)
		}
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "percentile_cont", Pos: position}}}
	var slots []int
	for _, fraction := range []string{".25", ".75", ".25"} {
		raw := parser.AggregateExpr{Name: "percentile_cont", Within: true, Pos: position, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NumericValue, Text: fraction, Pos: position}}}, Order: []parser.OrderKey{{Expr: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: position}}, Pos: position}}}
		node, err := b.bindOrderedSet(raw, name)
		if err != nil {
			t.Fatal(err)
		}
		slots = append(slots, node.(aggregateExpression).slot)
	}
	if slots[0] == slots[1] || slots[0] != slots[2] {
		t.Fatal("fraction identity", slots)
	}
}

func TestOrderedSetAggregateInternalCharCarrier(t *testing.T) {
	for _, operation := range []aggregateOperation{aggregateMode, aggregatePercentileDisc} {
		p := orderedSetUnitProvider(t, operation, sqltype.Char, false)
		in := orderedSetUnitInput(t, p, "b", "b", "c")
		direct, _ := sqltype.Float(p.fraction, .5)
		out, err := orderedSetUnitFinalize(context.Background(), &p, direct, in, sqltype.InputEnvironment{})
		if err != nil || out.Type() != p.input {
			t.Fatal("internal CHAR descriptor", out.Type(), err)
		}
		text, err := p.input.Output(context.Background(), out)
		if err != nil || text != "b" {
			t.Fatal(text, err)
		}
	}
}

// Exercise the real executor/provider boundary: a provider-only early return
// cannot prove that the executor avoided its preceding sort.
func TestOrderedSetAggregatePreSortDecisions(t *testing.T) {
	for _, family := range []struct {
		name      string
		operation aggregateOperation
		id        sqltype.ID
	}{
		{"cont_float", aggregatePercentileCont, sqltype.Float8},
		{"cont_interval", aggregatePercentileCont, sqltype.Interval},
		{"disc", aggregatePercentileDisc, sqltype.Int4},
	} {
		for _, tc := range []struct {
			name, fraction, want                  string
			array, empty, inputError, sortControl bool
		}{
			{name: "scalar_null", fraction: "NULL"},
			{name: "scalar_invalid", fraction: "1.1", want: "percentile value 1.1 is not between 0 and 1"},
			{name: "scalar_invalid_empty", fraction: "1.1", want: "percentile value 1.1 is not between 0 and 1", empty: true},
			{name: "array_null", fraction: "NULL", array: true},
			{name: "array_empty", fraction: "{}", want: "{}", array: true},
			{name: "array_all_null", fraction: "{NULL,NULL}", want: "{NULL,NULL}", array: true},
			{name: "array_invalid", fraction: "{1.1,-.1}", want: "percentile value 1.1 is not between 0 and 1", array: true},
			{name: "array_invalid_empty", fraction: "{1.1,-.1}", array: true, empty: true},
			{name: "array_nan_first", fraction: "{NaN,1.1}", want: "percentile value NaN is not between 0 and 1", array: true},
			{name: "null_preserves_input_error", fraction: "NULL", inputError: true},
			{name: "invalid_preserves_input_error", fraction: "1.1", inputError: true},
			{name: "valid_enters_comparison", fraction: ".5", sortControl: true},
		} {
			t.Run(family.name+"/"+tc.name, func(t *testing.T) {
				provider := orderedSetUnitProvider(t, family.operation, family.id, tc.array)
				fractionType := provider.fraction
				if tc.array {
					fractionType = orderedSetUnitFractionArray(t, provider)
				}
				directValue := sqltype.Null(fractionType)
				if tc.fraction != "NULL" {
					var err error
					directValue, err = fractionType.Input(context.Background(), tc.fraction, -1)
					if err != nil {
						t.Fatal(err)
					}
				}
				inputCalls, directCalls := 0, 0
				inputError := errors.New("ordered-set input must be evaluated before direct finalization")
				argument := queryProbe(provider.input.Usage(), func(c *EvalContext) (sqltype.Value, error) {
					inputCalls++
					if tc.inputError && inputCalls == 2 {
						return sqltype.Value{}, inputError
					}
					return c.Row[0], nil
				})
				direct := queryProbe(fractionType.Usage(), func(*EvalContext) (sqltype.Value, error) { directCalls++; return directValue, nil })
				values := []string{"30", "10", "20"}
				if family.id == sqltype.Interval {
					values = []string{"30 days", "10 days", "20 days"}
				}
				child := &valuesNode{querySchema: querySchema{{Usage: provider.input.Usage()}}}
				if !tc.empty {
					for _, text := range values {
						value, err := provider.input.Input(context.Background(), text, -1)
						if err != nil {
							t.Fatal(err)
						}
						child.rows = append(child.rows, []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: provider.input.Usage()}, value: value}})
					}
				}
				call := aggregateCall{provider: &provider, argument: argument, direct: []BoundExpr{direct}, operation: family.operation, stageWidth: 2, order: []sortKey{{expression: argument, comparison: provider.comparison, slot: 0, cache: 0}}}
				node := &aggregateNode{querySchema: querySchema{{Usage: provider.usage}}, child: child, calls: []aggregateCall{call}}
				root := &projectNode{querySchema: node.querySchema, child: node, expressions: []BoundExpr{groupExpression{expressionInfo: expressionInfo{usage: provider.usage}, slot: 0, frame: 1}}, frame: 1}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: ".Comparison.Compare"}
				rows, count, err := finalizeQuery(probe, root, &querySource{}, nil)
				if rows != nil {
					defer rows.Close()
				}
				wantInputs := 3
				if tc.empty {
					wantInputs = 0
				}
				if tc.inputError {
					wantInputs = 2
				}
				wantDirect := 1
				if tc.inputError {
					wantDirect = 0
				}
				if inputCalls != wantInputs || directCalls != wantDirect {
					t.Fatalf("input/direct evaluation got %d/%d want %d/%d; rows=%v count=%d comparator=%t err=%v", inputCalls, directCalls, wantInputs, wantDirect, rows, count, probe.entered, err)
				}
				if tc.sortControl {
					if !probe.entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) {
						t.Fatal("comparator probe did not observe actual sort", probe.entered, rows, count, err)
					}
					return
				}
				if probe.entered {
					t.Fatal("terminal percentile decision entered sorting comparator", err)
				}
				if tc.inputError {
					if !errors.Is(err, inputError) || rows != nil || count != 0 {
						t.Fatal("lost input error/atomic publication", rows, count, err)
					}
					return
				}
				if strings.HasPrefix(tc.want, "percentile value") {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != tc.want || diagnostic.Position != "" || rows != nil || count != 0 {
						t.Fatal("fraction validation/atomic publication", rows, count, err)
					}
					return
				}
				if err != nil || rows == nil || count != 1 {
					t.Fatal("terminal result", rows, count, err)
				}
				out := make([]sqltype.Value, 1)
				present, err := rows.Next(context.Background(), out)
				if !present || err != nil || out[0].Type() != provider.usage.Type {
					t.Fatal("result authority", present, out, err)
				}
				if tc.want == "" {
					if !out[0].IsNull() {
						t.Fatal("expected SQL NULL", out[0])
					}
				} else {
					text, err := provider.usage.Type.Output(context.Background(), out[0])
					if err != nil || text != tc.want {
						t.Fatal(text, tc.want, err)
					}
				}
			})
		}
	}
}

func TestOrderedSetAggregateResultAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.os_unit_enum AS ENUM('z','a')"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.os_unit_domain AS INT"))
	view := e.CatalogRead()
	enum := domainUnitType(t, view, "os_unit_enum").typ
	domain := domainUnitType(t, view, "os_unit_domain").typ
	for _, tc := range []struct {
		name            string
		operation       aggregateOperation
		id              sqltype.ID
		custom          *sqltype.Descriptor
		array, explicit bool
	}{
		{"cont_float", aggregatePercentileCont, sqltype.Float8, nil, false, false},
		{"cont_interval", aggregatePercentileCont, sqltype.Interval, nil, false, false},
		{"cont_float_array", aggregatePercentileCont, sqltype.Float8, nil, true, false},
		{"cont_interval_array", aggregatePercentileCont, sqltype.Interval, nil, true, false},
		{"disc_sized", aggregatePercentileDisc, sqltype.VarChar, nil, false, false},
		{"disc_sized_array", aggregatePercentileDisc, sqltype.VarChar, nil, true, false},
		{"mode_explicit", aggregateMode, sqltype.VarChar, nil, false, true},
		{"disc_enum", aggregatePercentileDisc, 0, enum, false, false},
		{"disc_enum_array", aggregatePercentileDisc, 0, enum, true, false},
		{"mode_enum", aggregateMode, 0, enum, false, false},
		{"disc_domain", aggregatePercentileDisc, 0, domain, false, false},
		{"disc_domain_array", aggregatePercentileDisc, 0, domain, true, false},
		{"mode_domain", aggregateMode, 0, domain, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			e.mu.Lock()
			auth, err := e.authorizationLocked(b.op)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			b.queryAuth = &auth
			// Resolve expectations and bind against this one coherent generation,
			// not custom descriptor pointers retained from an external catalog read.
			catalog := b.bindingCatalog()
			id := tc.id
			if tc.custom != nil {
				id = tc.custom.ID()
			}
			input, ok := catalog.LookupType(id)
			if !ok {
				t.Fatal("binding input authority", id)
			}
			descriptor, ok := catalog.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)})
			if !ok {
				t.Fatal("binding input owner")
			}
			inputOwner := descriptor.(*typeDescriptor)
			if inputOwner.typ != input {
				t.Fatal("input owner/leaf generation", inputOwner, input)
			}
			namespace, ok := catalog.Descriptor(inputOwner.Namespace())
			wantNamespace := "pg_catalog"
			if tc.custom != nil {
				wantNamespace = "public"
			}
			if !ok || namespace.Name() != wantNamespace {
				t.Fatal("input namespace authority", namespace, wantNamespace)
			}
			if tc.custom != nil && inputOwner.owner != auth.state.CurrentUser {
				t.Fatal("custom input principal authority", inputOwner.owner, auth.state.CurrentUser)
			}
			usage := input.Usage()
			if tc.id == sqltype.VarChar {
				usage.Typmod = 8
				usage.Collation = sqltype.CollationRef{ID: 950}
			}
			b.aggregateClause = "SELECT"
			b.parameters[1] = usage
			position := parser.Position{Character: 8}
			var key parser.Expr = parser.ParameterExpr{Number: 1, Pos: position}
			if tc.explicit {
				key = parser.CollateExpr{Child: key, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "POSIX", Pos: position}}}, Pos: position}
			}
			name := "percentile_cont"
			if tc.operation == aggregatePercentileDisc {
				name = "percentile_disc"
			}
			if tc.operation == aggregateMode {
				name = "mode"
			}
			raw := parser.AggregateExpr{Name: name, Within: true, Pos: position, Order: []parser.OrderKey{{Expr: key, Pos: position}}}
			if tc.operation != aggregateMode {
				direct := parser.TypeName{Name: "float8"}
				text := ".5"
				if tc.array {
					direct.ArrayBounds = []int32{0}
					text = "{.5}"
				}
				raw.Args = []parser.Expr{parser.TypedLiteralExpr{TypeName: direct, Text: text, Pos: position}}
			}
			result, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			call := b.aggregates[0]
			argument := call.argument
			if b.bindingCatalog().state != catalog.state || argument.ResultUsage().Type != input {
				t.Fatal("input lost coherent binding generation", argument.ResultUsage(), input)
			}
			want := argument.ResultUsage()
			want.Typmod = -1
			if tc.array {
				array, ok := catalog.LookupArray(input)
				if !ok || array.Element() != input || array.ID() != inputOwner.array {
					t.Fatal("catalog associated-array authority", array, input, inputOwner)
				}
				catalogLeaf, ok := catalog.LookupType(array.Element().ID())
				if !ok || catalogLeaf != input {
					t.Fatal("catalog array leaf generation", catalogLeaf, input)
				}
				want.Type = array
			}
			provider := call.provider.(*orderedSetProvider)
			actual := result.ResultUsage()
			if actual != want || provider.ResultUsage() != want || provider.input != input {
				t.Fatal("complete exact Usage/provider authority", actual, want, provider)
			}
			descriptor, ok = catalog.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(actual.Type.ID())})
			if !ok {
				t.Fatal("actual result owner")
			}
			resultOwner := descriptor.(*typeDescriptor)
			if actual.Type != resultOwner.typ || resultOwner.Namespace() != inputOwner.Namespace() || resultOwner.owner != inputOwner.owner {
				t.Fatal("result generation/namespace/principal authority", actual, resultOwner, inputOwner)
			}
			if tc.array && (actual.Type.Element() != input || actual.Type.Element() != provider.input || resultOwner.typ.Element() != input || inputOwner.array != actual.Type.ID()) {
				t.Fatal("actual result/leaf/owner relationship", actual, input, inputOwner)
			}
			for _, comparison := range []sqltype.Comparison{provider.comparison, call.order[0].comparison} {
				collation, indeterminate := comparison.InputCollation()
				if collation != argument.ResultUsage().Collation || indeterminate != argument.CollationIndeterminate() {
					t.Fatal("actual comparison collation authority", collation, indeterminate, argument.ResultUsage(), argument.CollationIndeterminate())
				}
			}
			if !result.Properties().StageDependent {
				t.Fatal("missing aggregate stage dependence")
			}
			if result.CollationIndeterminate() != argument.CollationIndeterminate() || result.ExplicitCollation() != argument.ExplicitCollation() {
				t.Fatal("lost collation provenance", result, argument)
			}
			left, right, pos := result.ImplicitCollationConflict()
			if result.CollationIndeterminate() || left != (sqltype.CollationRef{}) || right != (sqltype.CollationRef{}) || pos != (parser.Position{}) {
				t.Fatal("invented conflict provenance", left, right, pos)
			}
			if tc.explicit && (result.ExplicitCollation() == (parser.Position{}) || result.ResultUsage().Collation.ID != 951) {
				t.Fatal("explicit POSIX authority", result.ResultUsage(), result.ExplicitCollation())
			}
			if !tc.explicit && result.ExplicitCollation() != (parser.Position{}) {
				t.Fatal("invented explicit collation")
			}
			if tc.id == sqltype.VarChar && (usage.Typmod != 8 || result.ResultUsage().Typmod != -1) {
				t.Fatal("declared sized source/result typmod", usage, result.ResultUsage())
			}
		})
	}
	// Bind real catalog columns privately; input origins exist, but aggregate
	// outputs are computed descriptors, including enum/domain associated arrays.
	table := domainUnitTable(t, e, op, `CREATE TABLE public.os_unit_authority(v VARCHAR(4) COLLATE "C",w VARCHAR(4) COLLATE "POSIX",n NUMERIC(10,4),i INTERVAL,e os_unit_enum,d os_unit_domain)`)
	if table.columns[0].Usage.Typmod != 8 || table.columns[0].Usage.Collation.ID != 950 {
		t.Fatal("genuine sized/collated source descriptor", table.columns[0])
	}
	for _, expression := range []string{
		"percentile_cont(.5) WITHIN GROUP(ORDER BY n)",
		"percentile_cont(.5) WITHIN GROUP(ORDER BY i)",
		"percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY n)",
		"percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY i)",
		"percentile_disc(.5) WITHIN GROUP(ORDER BY v)",
		"percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY v)",
		"mode() WITHIN GROUP(ORDER BY v)",
		"percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY e)",
		"percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY d)",
	} {
		q := domainUnitBindSelect(t, e, op, "SELECT v,"+expression+" FROM os_unit_authority GROUP BY v")
		schema := q.Root.Schema()
		if schema[0].OriginTable != uint32(table.address.ObjectID) || schema[0].OriginAttribute != 1 {
			t.Fatal("missing genuine input origin", schema[0])
		}
		column := schema[1]
		if column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || !column.NullabilityKnown || !column.Nullable {
			t.Fatal("computed aggregate origin/schema", expression, column)
		}
		call := q.Root.(*projectNode).child.(*aggregateNode).calls[0]
		if column.Usage != call.provider.ResultUsage() {
			t.Fatal("schema/provider authority drift", column, call.provider.ResultUsage())
		}
		if strings.Contains(expression, "ORDER BY v)") && column.Usage.Collation != table.columns[0].Usage.Collation {
			t.Fatal("lost source-column collation authority", expression, column.Usage, table.columns[0].Usage)
		}
		provider := call.provider.(*orderedSetProvider)
		for _, comparison := range []sqltype.Comparison{provider.comparison, call.order[0].comparison} {
			collation, indeterminate := comparison.InputCollation()
			if collation != call.argument.ResultUsage().Collation || indeterminate != call.argument.CollationIndeterminate() {
				t.Fatal("declared-column comparison authority", expression, collation, indeterminate, call.argument.ResultUsage())
			}
		}
	}
	// The implicit C/POSIX conflict is rejected, never turned into a successful
	// result with fabricated collation authority.
	raw := statement(t, "SELECT mode() WITHIN GROUP(ORDER BY COALESCE(v,w)) FROM os_unit_authority").(*parser.Select)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.queryAuth = &auth
	if err == nil {
		_, err = b.bindSelect(raw, false)
	}
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P21" || diagnostic.Message != `collation mismatch between implicit collations "C" and "POSIX"` || diagnostic.Hint != "You can choose the collation by applying the COLLATE clause to one or both expressions." {
		t.Fatal("implicit conflict authority", err)
	}
	q := domainUnitBindSelect(t, e, op, `SELECT mode() WITHIN GROUP(ORDER BY COALESCE(v,w) COLLATE "C") FROM os_unit_authority`)
	if q.Root.Schema()[0].Usage.Collation.ID != 950 {
		t.Fatal("explicit conflict resolution authority", q.Root.Schema())
	}
}
