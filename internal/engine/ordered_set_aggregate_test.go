package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func orderedSetUnitProvider(t *testing.T, operation functions.AggregateOperation, id sqltype.ID, array bool) *functions.OrderedSet {
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
	p, err := functions.NewOrderedSet(usage, input, fraction, operation, array, comparison)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// orderedSetVariant rebuilds a unit provider with another operation, array
// shape or result usage over the same input, fraction and comparison.
func orderedSetVariant(t *testing.T, p *functions.OrderedSet, operation functions.AggregateOperation, array bool, usage sqltype.Usage) *functions.OrderedSet {
	t.Helper()
	variant, err := functions.NewOrderedSet(usage, p.InputType(), p.FractionType(), operation, array, p.Comparison())
	if err != nil {
		t.Fatal(err)
	}
	return variant
}

func orderedSetUnitInput(t *testing.T, p *functions.OrderedSet, texts ...string) functions.OrderedInput {
	t.Helper()
	in := functions.OrderedInput{Width: 2, Indices: make([]int, len(texts)), Values: make([]sqltype.Value, len(texts)*2)}
	for i, text := range texts {
		value, err := p.InputType().Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		in.Indices[i] = i
		in.Values[i*2] = value
	}
	return in
}

func orderedSetUnitFractionArray(t *testing.T, p *functions.OrderedSet) *sqltype.Descriptor {
	t.Helper()
	// Pair the executable codec with this provider's exact leaf generation.
	array, err := sqltype.Array(p.FractionType().AssociatedArray(), "_float8", p.FractionType())
	if err != nil {
		t.Fatal(err)
	}
	return array
}

func orderedSetUnitFinalize(ctx context.Context, p *functions.OrderedSet, direct sqltype.Value, in functions.OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	prepared, err := p.PrepareOrdered(ctx, []sqltype.Value{direct}, in.Len())
	if err != nil {
		return sqltype.Value{}, err
	}
	if prepared.Terminal {
		return prepared.Value, ctx.Err()
	}
	return p.FinalizeOrdered(ctx, prepared, in, env)
}

func TestOrderedSetAggregateSevenProviders(t *testing.T) {
	for _, tc := range []struct {
		operation functions.AggregateOperation
		id        sqltype.ID
		array     bool
		want      string
	}{
		{functions.AggregatePercentileCont, sqltype.Float8, false, "25"}, {functions.AggregatePercentileCont, sqltype.Interval, false, "25 days"},
		{functions.AggregatePercentileCont, sqltype.Float8, true, "{25}"}, {functions.AggregatePercentileCont, sqltype.Interval, true, "{\"25 days\"}"},
		{functions.AggregatePercentileDisc, sqltype.Int4, false, "20"}, {functions.AggregatePercentileDisc, sqltype.Int4, true, "{20}"}, {functions.AggregateMode, sqltype.Int4, false, "10"},
	} {
		p := orderedSetUnitProvider(t, tc.operation, tc.id, tc.array)
		texts := []string{"10", "20", "30", "40"}
		if tc.id == sqltype.Interval {
			texts = []string{"10 days", "20 days", "30 days", "40 days"}
		}
		in := orderedSetUnitInput(t, p, texts...)
		before := append([]sqltype.Value(nil), in.Values...)
		direct, _ := sqltype.Float(p.FractionType(), .5)
		if tc.array {
			var err error
			direct, err = orderedSetUnitFractionArray(t, p).Input(context.Background(), "{.5}", -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		value, err := orderedSetUnitFinalize(context.Background(), p, direct, in, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := p.ResultUsage().Type.Output(context.Background(), value)
		if err != nil || text != tc.want {
			t.Fatalf("%v %q want %q: %v", tc, text, tc.want, err)
		}
		if !reflect.DeepEqual(before, in.Values) {
			t.Fatal("borrowed payload mutated")
		}
	}
}

func TestOrderedSetAggregateShapeAndEmptyPrecedence(t *testing.T) {
	p := orderedSetUnitProvider(t, functions.AggregatePercentileCont, sqltype.Float8, true)
	fractionArray := orderedSetUnitFractionArray(t, p)
	direct, err := fractionArray.Input(context.Background(), "[-1:0][3:5]={{1,NULL,0},{.5,.25,.5}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	in := orderedSetUnitInput(t, p, "10", "20", "30", "40")
	out, err := orderedSetUnitFinalize(context.Background(), p, direct, in, sqltype.InputEnvironment{})
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
	empty, err := orderedSetUnitFinalize(context.Background(), p, invalid, functions.OrderedInput{}, sqltype.InputEnvironment{})
	if err != nil || !empty.IsNull() {
		t.Fatal("array validated empty state", err)
	}
	_, err = orderedSetUnitFinalize(context.Background(), p, invalid, in, sqltype.InputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22003" || d.Message != "percentile value 1.1 is not between 0 and 1" || d.Position != "" {
		t.Fatal(err)
	}
	p = orderedSetVariant(t, p, p.Operation(), false, p.InputType().Usage())
	bad, _ := sqltype.Float(p.FractionType(), 1.1)
	_, err = orderedSetUnitFinalize(context.Background(), p, bad, functions.OrderedInput{}, sqltype.InputEnvironment{})
	if !errors.As(err, &d) || d.Code != "22003" {
		t.Fatal("scalar skipped validation", err)
	}
}

func TestOrderedSetAggregateIEEEAndModeEquality(t *testing.T) {
	p := orderedSetUnitProvider(t, functions.AggregatePercentileCont, sqltype.Float8, false)
	in := orderedSetUnitInput(t, p, "Infinity", "Infinity")
	for _, fraction := range []float64{0, .5, 1} {
		direct, _ := sqltype.Float(p.FractionType(), fraction)
		out, err := orderedSetUnitFinalize(context.Background(), p, direct, in, sqltype.InputEnvironment{})
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
		mode := orderedSetUnitProvider(t, functions.AggregateMode, tc.id, false)
		out, err := orderedSetUnitFinalize(context.Background(), mode, sqltype.Value{}, orderedSetUnitInput(t, mode, tc.values...), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		want, err := mode.InputType().Input(context.Background(), tc.want, -1)
		if err != nil {
			t.Fatal(err)
		}
		cmp, err := mode.Comparison().Compare(context.Background(), out, want, sqltype.InputEnvironment{})
		if err != nil || cmp != 0 {
			t.Fatal(tc, cmp, err)
		}
	}
}

func TestOrderedSetAggregateCancellationPhases(t *testing.T) {
	for _, phase := range []string{".(*OrderedSet).mode", ".(*OrderedSet).PrepareOrdered", ".(*OrderedSet).FinalizeOrdered", ".(*OrderedSet).percentile"} {
		t.Run(phase, func(t *testing.T) {
			operation := functions.AggregatePercentileCont
			if phase == ".(*OrderedSet).mode" {
				operation = functions.AggregateMode
			}
			p := orderedSetUnitProvider(t, operation, sqltype.Float8, false)
			in := orderedSetUnitInput(t, p, "10", "20", "30", "40")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: phase}
			direct, _ := sqltype.Float(p.FractionType(), .5)
			_, err := orderedSetUnitFinalize(probe, p, direct, in, sqltype.InputEnvironment{})
			if !probe.entered || !errors.Is(err, context.Canceled) {
				t.Fatal(phase, probe.entered, err)
			}
		})
	}
}

func TestOrderedSetAggregateGroupFrameAndPublication(t *testing.T) {
	p := orderedSetUnitProvider(t, functions.AggregatePercentileCont, sqltype.Float8, false)
	info := expr.Info{Usage: p.InputType().Usage(), Property: expr.Properties{RowDependent: true}}
	key := plan.ColumnExpr{Info: info, Scope: 1, Frame: 1, Slot: 0}
	arg := plan.ColumnExpr{Info: info, Scope: 1, Frame: 1, Slot: 1}
	grouping, err := sqltype.BindGrouping(key.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	literal := func(n float64) expr.Expr {
		value, _ := sqltype.Float(p.InputType(), n)
		return plan.LiteralExpr{Info: expr.Info{Usage: p.InputType().Usage()}, Value: value}
	}
	for _, phase := range []string{"success", "having", "direct", "prepare", "sort", "scan", "array", "percentile"} {
		t.Run(phase, func(t *testing.T) {
			provider := p
			if phase == "scan" {
				provider = orderedSetVariant(t, provider, functions.AggregateMode, provider.Array(), provider.ResultUsage())
			}
			if phase == "array" || phase == "prepare" {
				usage := provider.ResultUsage()
				usage.Type = orderedSetUnitFractionArray(t, provider)
				provider = orderedSetVariant(t, provider, provider.Operation(), true, usage)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var execution context.Context = ctx
			target := ""
			switch phase {
			case "prepare":
				target = ".(*OrderedSet).PrepareOrdered"
			case "sort":
				target = ".sortOrderedAggregate.func1"
			case "scan":
				target = ".(*OrderedSet).mode"
			case "array":
				target = ".(*OrderedSet).arrayPercentiles"
			case "percentile":
				target = ".(*OrderedSet).percentile"
			}
			var probe *aggregateModifierFamilyCancelContext
			if target != "" {
				probe = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = probe
			}
			calls := 0
			direct := queryProbe(provider.FractionType().Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if phase == "direct" {
					cancel()
				}
				if provider.Array() {
					return orderedSetUnitFractionArray(t, provider).Input(c.Context, "{.25,.75}", -1)
				}
				return plan.GroupExpr{Info: expr.Info{Usage: provider.FractionType().Usage()}, Slot: 0, Frame: 1}.Eval(c)
			})
			child := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: p.InputType().Usage()}, {Usage: p.InputType().Usage()}}, Rows: [][]expr.Expr{{literal(.25), literal(10)}, {literal(.25), literal(20)}, {literal(.75), literal(30)}, {literal(.75), literal(40)}}}
			call := plan.AggregateCall{Provider: provider, Argument: arg, Direct: []expr.Expr{direct}, Operation: provider.Operation(), StageWidth: 2, Order: []plan.SortKey{{Expression: arg, Comparison: provider.Comparison(), Slot: 0, Cache: 0}}}
			aggregate := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Usage: p.InputType().Usage()}, {Usage: provider.ResultUsage()}}, Child: child, Keys: []expr.Expr{key}, Grouping: []plan.Grouping{{Grouping: grouping}}, Calls: []plan.AggregateCall{call}}
			root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: provider.ResultUsage()}}, Child: aggregate, Expressions: []expr.Expr{plan.GroupExpr{Info: expr.Info{Usage: provider.ResultUsage()}, Slot: 1, Frame: 1}}, Frame: 1}
			if phase == "having" {
				boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
				root.Child = &plan.FilterNode{QuerySchema: aggregate.QuerySchema, Child: aggregate, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
			}
			owned := &exec.Source{Anonymous: [][]sqltype.Value{{sqltype.Null(p.InputType())}}, Flat: []sqltype.Value{sqltype.Null(p.InputType())}}
			source := &exec.Source{Sources: []*exec.Source{owned}}
			rows, count, err := exec.Finalize(execution, root, source, nil)
			if phase == "having" {
				if err != nil || rows == nil || count != 0 || calls != 2 {
					t.Fatal("HAVING bypassed ordered-set finalization", calls, count, err)
				}
				rows.Close()
				return
			}
			if phase != "success" {
				if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.Sources != nil || owned.Flat != nil {
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
	b.Clause.AggregateClause = "SELECT"
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
		node, _, err := b.BindRegisteredAggregate(raw, name, orderedSetDefinition())
		if err != nil {
			t.Fatal(tc, err)
		}
		if got := node.ResultUsage().Type.DiagnosticName(); got != tc.result {
			t.Fatal(tc, got)
		}
		call := b.Grouping.Aggregates[node.(plan.AggregateExpr).Slot]
		if call.Order[0].Slot != 0 || call.Order[0].Cache != 0 || call.StageWidth != 2 || (call.Direct == nil) != (tc.name == "mode") {
			t.Fatal("uncached input/direct identity", tc, call)
		}
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "percentile_cont", Pos: position}}}
	var slots []int
	for _, fraction := range []string{".25", ".75", ".25"} {
		raw := parser.AggregateExpr{Name: "percentile_cont", Within: true, Pos: position, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NumericValue, Text: fraction, Pos: position}}}, Order: []parser.OrderKey{{Expr: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: position}}, Pos: position}}}
		node, _, err := b.BindRegisteredAggregate(raw, name, orderedSetDefinition())
		if err != nil {
			t.Fatal(err)
		}
		slots = append(slots, node.(plan.AggregateExpr).Slot)
	}
	if slots[0] == slots[1] || slots[0] != slots[2] {
		t.Fatal("fraction identity", slots)
	}
}

func TestOrderedSetAggregateInternalCharCarrier(t *testing.T) {
	for _, operation := range []functions.AggregateOperation{functions.AggregateMode, functions.AggregatePercentileDisc} {
		p := orderedSetUnitProvider(t, operation, sqltype.Char, false)
		in := orderedSetUnitInput(t, p, "b", "b", "c")
		direct, _ := sqltype.Float(p.FractionType(), .5)
		out, err := orderedSetUnitFinalize(context.Background(), p, direct, in, sqltype.InputEnvironment{})
		if err != nil || out.Type() != p.InputType() {
			t.Fatal("internal CHAR descriptor", out.Type(), err)
		}
		text, err := p.InputType().Output(context.Background(), out)
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
		operation functions.AggregateOperation
		id        sqltype.ID
	}{
		{"cont_float", functions.AggregatePercentileCont, sqltype.Float8},
		{"cont_interval", functions.AggregatePercentileCont, sqltype.Interval},
		{"disc", functions.AggregatePercentileDisc, sqltype.Int4},
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
				fractionType := provider.FractionType()
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
				argument := queryProbe(provider.InputType().Usage(), func(c *exec.Context) (sqltype.Value, error) {
					inputCalls++
					if tc.inputError && inputCalls == 2 {
						return sqltype.Value{}, inputError
					}
					return c.Row[0], nil
				})
				direct := queryProbe(fractionType.Usage(), func(*exec.Context) (sqltype.Value, error) { directCalls++; return directValue, nil })
				values := []string{"30", "10", "20"}
				if family.id == sqltype.Interval {
					values = []string{"30 days", "10 days", "20 days"}
				}
				child := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: provider.InputType().Usage()}}}
				if !tc.empty {
					for _, text := range values {
						value, err := provider.InputType().Input(context.Background(), text, -1)
						if err != nil {
							t.Fatal(err)
						}
						child.Rows = append(child.Rows, []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: provider.InputType().Usage()}, Value: value}})
					}
				}
				call := plan.AggregateCall{Provider: provider, Argument: argument, Direct: []expr.Expr{direct}, Operation: family.operation, StageWidth: 2, Order: []plan.SortKey{{Expression: argument, Comparison: provider.Comparison(), Slot: 0, Cache: 0}}}
				node := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Usage: provider.ResultUsage()}}, Child: child, Calls: []plan.AggregateCall{call}}
				root := &plan.ProjectNode{QuerySchema: node.QuerySchema, Child: node, Expressions: []expr.Expr{plan.GroupExpr{Info: expr.Info{Usage: provider.ResultUsage()}, Slot: 0, Frame: 1}}, Frame: 1}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: ".Comparison.Compare"}
				rows, count, err := exec.Finalize(probe, root, &exec.Source{}, nil)
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
				if !present || err != nil || out[0].Type() != provider.ResultUsage().Type {
					t.Fatal("result authority", present, out, err)
				}
				if tc.want == "" {
					if !out[0].IsNull() {
						t.Fatal("expected SQL NULL", out[0])
					}
				} else {
					text, err := provider.ResultUsage().Type.Output(context.Background(), out[0])
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
	enum := domainUnitType(t, view, "os_unit_enum").SQLType
	domain := domainUnitType(t, view, "os_unit_domain").SQLType
	for _, tc := range []struct {
		name            string
		operation       functions.AggregateOperation
		id              sqltype.ID
		custom          *sqltype.Descriptor
		array, explicit bool
	}{
		{"cont_float", functions.AggregatePercentileCont, sqltype.Float8, nil, false, false},
		{"cont_interval", functions.AggregatePercentileCont, sqltype.Interval, nil, false, false},
		{"cont_float_array", functions.AggregatePercentileCont, sqltype.Float8, nil, true, false},
		{"cont_interval_array", functions.AggregatePercentileCont, sqltype.Interval, nil, true, false},
		{"disc_sized", functions.AggregatePercentileDisc, sqltype.VarChar, nil, false, false},
		{"disc_sized_array", functions.AggregatePercentileDisc, sqltype.VarChar, nil, true, false},
		{"mode_explicit", functions.AggregateMode, sqltype.VarChar, nil, false, true},
		{"disc_enum", functions.AggregatePercentileDisc, 0, enum, false, false},
		{"disc_enum_array", functions.AggregatePercentileDisc, 0, enum, true, false},
		{"mode_enum", functions.AggregateMode, 0, enum, false, false},
		{"disc_domain", functions.AggregatePercentileDisc, 0, domain, false, false},
		{"disc_domain_array", functions.AggregatePercentileDisc, 0, domain, true, false},
		{"mode_domain", functions.AggregateMode, 0, domain, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			e.mu.Lock()
			auth, err := e.authorizationLocked(binderOperation(b))
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			b.Query.Auth = &auth
			// Resolve expectations and bind against this one coherent generation,
			// not custom descriptor pointers retained from an external catalog read.
			catalogView := b.BindingCatalog()
			id := tc.id
			if tc.custom != nil {
				id = tc.custom.ID()
			}
			input, ok := catalogView.LookupType(id)
			if !ok {
				t.Fatal("binding input authority", id)
			}
			descriptor, ok := catalogView.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
			if !ok {
				t.Fatal("binding input owner")
			}
			inputOwner := descriptor.(*catalog.Type)
			if inputOwner.SQLType != input {
				t.Fatal("input owner/leaf generation", inputOwner, input)
			}
			namespace, ok := catalogView.Descriptor(inputOwner.Namespace())
			wantNamespace := "pg_catalog"
			if tc.custom != nil {
				wantNamespace = "public"
			}
			if !ok || namespace.Name() != wantNamespace {
				t.Fatal("input namespace authority", namespace, wantNamespace)
			}
			if tc.custom != nil && inputOwner.Owner != auth.State.CurrentUser {
				t.Fatal("custom input principal authority", inputOwner.Owner, auth.State.CurrentUser)
			}
			usage := input.Usage()
			if tc.id == sqltype.VarChar {
				usage.Typmod = 8
				usage.Collation = sqltype.CollationRef{ID: 950}
			}
			b.Clause.AggregateClause = "SELECT"
			b.Params.Parameters[1] = usage
			position := parser.Position{Character: 8}
			var key parser.Expr = parser.ParameterExpr{Number: 1, Pos: position}
			if tc.explicit {
				key = parser.CollateExpr{Child: key, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "POSIX", Pos: position}}}, Pos: position}
			}
			name := "percentile_cont"
			if tc.operation == functions.AggregatePercentileDisc {
				name = "percentile_disc"
			}
			if tc.operation == functions.AggregateMode {
				name = "mode"
			}
			raw := parser.AggregateExpr{Name: name, Within: true, Pos: position, Order: []parser.OrderKey{{Expr: key, Pos: position}}}
			if tc.operation != functions.AggregateMode {
				direct := parser.TypeName{Name: "float8"}
				text := ".5"
				if tc.array {
					direct.ArrayBounds = []int32{0}
					text = "{.5}"
				}
				raw.Args = []parser.Expr{parser.TypedLiteralExpr{TypeName: direct, Text: text, Pos: position}}
			}
			result, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			call := b.Grouping.Aggregates[0]
			argument := call.Argument
			if b.BindingCatalog().State() != catalogView.State() || argument.ResultUsage().Type != input {
				t.Fatal("input lost coherent binding generation", argument.ResultUsage(), input)
			}
			want := argument.ResultUsage()
			want.Typmod = -1
			if tc.array {
				array, ok := catalogView.LookupArray(input)
				if !ok || array.Element() != input || array.ID() != inputOwner.Array {
					t.Fatal("catalog associated-array authority", array, input, inputOwner)
				}
				catalogLeaf, ok := catalogView.LookupType(array.Element().ID())
				if !ok || catalogLeaf != input {
					t.Fatal("catalog array leaf generation", catalogLeaf, input)
				}
				want.Type = array
			}
			provider := call.Provider.(*functions.OrderedSet)
			actual := result.ResultUsage()
			if actual != want || provider.ResultUsage() != want || provider.InputType() != input {
				t.Fatal("complete exact Usage/provider authority", actual, want, provider)
			}
			descriptor, ok = catalogView.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(actual.Type.ID())})
			if !ok {
				t.Fatal("actual result owner")
			}
			resultOwner := descriptor.(*catalog.Type)
			if actual.Type != resultOwner.SQLType || resultOwner.Namespace() != inputOwner.Namespace() || resultOwner.Owner != inputOwner.Owner {
				t.Fatal("result generation/namespace/principal authority", actual, resultOwner, inputOwner)
			}
			if tc.array && (actual.Type.Element() != input || actual.Type.Element() != provider.InputType() || resultOwner.SQLType.Element() != input || inputOwner.Array != actual.Type.ID()) {
				t.Fatal("actual result/leaf/owner relationship", actual, input, inputOwner)
			}
			for _, comparison := range []sqltype.Comparison{provider.Comparison(), call.Order[0].Comparison} {
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
	if table.Columns[0].Usage.Typmod != 8 || table.Columns[0].Usage.Collation.ID != 950 {
		t.Fatal("genuine sized/collated source descriptor", table.Columns[0])
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
		if schema[0].OriginTable != uint32(table.Address().ObjectID) || schema[0].OriginAttribute != 1 {
			t.Fatal("missing genuine input origin", schema[0])
		}
		column := schema[1]
		if column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || !column.NullabilityKnown || !column.Nullable {
			t.Fatal("computed aggregate origin/schema", expression, column)
		}
		call := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls[0]
		if column.Usage != call.Provider.ResultUsage() {
			t.Fatal("schema/provider authority drift", column, call.Provider.ResultUsage())
		}
		if strings.Contains(expression, "ORDER BY v)") && column.Usage.Collation != table.Columns[0].Usage.Collation {
			t.Fatal("lost source-column collation authority", expression, column.Usage, table.Columns[0].Usage)
		}
		provider := call.Provider.(*functions.OrderedSet)
		for _, comparison := range []sqltype.Comparison{provider.Comparison(), call.Order[0].Comparison} {
			collation, indeterminate := comparison.InputCollation()
			if collation != call.Argument.ResultUsage().Collation || indeterminate != call.Argument.CollationIndeterminate() {
				t.Fatal("declared-column comparison authority", expression, collation, indeterminate, call.Argument.ResultUsage())
			}
		}
	}
	// The implicit C/POSIX conflict is rejected, never turned into a successful
	// result with fabricated collation authority.
	raw := statement(t, "SELECT mode() WITHIN GROUP(ORDER BY COALESCE(v,w)) FROM os_unit_authority").(*parser.Select)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	if err == nil {
		_, err = b.BindSelect(raw, false)
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

// orderedSetDefinition is the registry entry shared by the WITHIN GROUP
// aggregates.
func orderedSetDefinition() functions.AggregateDefinition {
	definition, _ := functions.LookupAggregate("percentile_cont")
	return definition
}
