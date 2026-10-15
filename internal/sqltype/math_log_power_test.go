package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func logPowerPlan(t *testing.T, name string, types map[ID]*Descriptor, id ID, inputs ...string) (MathPlan, []Value) {
	t.Helper()
	args := make([]Usage, len(inputs))
	values := make([]Value, len(inputs))
	for i, text := range inputs {
		d := types[id]
		if name == "width_bucket" && i == 3 {
			d = types[Int4]
		}
		args[i] = d.Usage()
		var err error
		values[i], err = d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
	}
	result := types[id]
	if name == "width_bucket" {
		result = types[Int4]
	}
	p, err := BindMath(name, result.Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return p, values
}

func TestMathLogPowerProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		id     ID
		inputs []string
		want   string
	}{
		{"ln", Numeric, []string{"2.0"}, "0.6931471805599453"},
		{"ln", Numeric, []string{"1.000000000000000000000000000001"}, "0.0000000000000000000000000000010000000000000000"},
		{"log", Numeric, []string{"2.0", "64.0"}, "6.0000000000000000"},
		{"log10", Numeric, []string{"1000"}, "3.0000000000000000"},
		{"sqrt", Numeric, []string{"4.0000"}, "2.000000000000000"},
		{"power", Numeric, []string{"9007199254740993", "2"}, "81129638414606699710187514626049"},
		{"width_bucket", Numeric, []string{"0.58", "0", "1", "50"}, "30"},
		{"width_bucket", Numeric, []string{"9007199254740993", "9007199254740992", "9007199254740994", "2"}, "2"},
		{"width_bucket", Float8, []string{"0", "-1e308", "1e308", "10"}, "6"},
		{"width_bucket", Float8, []string{"0.9999999999999999", "0", "1", "2147483647"}, "2147483647"},
		{"width_bucket", Float8, []string{"8", "10", "0", "5"}, "2"},
	} {
		p, args := logPowerPlan(t, tc.name, types, tc.id, tc.inputs...)
		original := append([]Value(nil), args...)
		got, err := p.Evaluate(ctx, args, InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		text, err := got.typ.Output(ctx, got)
		if err != nil || text != tc.want || got.typ != p.result.Type || !reflect.DeepEqual(args, original) {
			t.Fatal(tc, text, err)
		}
		for i := range args {
			saved := args[i]
			args[i] = Null(saved.typ)
			got, err = p.Evaluate(ctx, args, InputEnvironment{})
			if err != nil || got != Null(p.result.Type) {
				t.Fatal("strict argument", tc.name, i, got, err)
			}
			args[i] = saved
		}
	}
	for _, tc := range []struct {
		name, input string
		want        float64
	}{
		{"lgamma", "-0.5", 1.2655121234846454}, {"ln", "2", 0.6931471805599453}, {"sqrt", "2", 1.4142135623730951},
		{"lgamma", "-Infinity", math.Inf(1)}, {"sqrt", "-0", math.Copysign(0, -1)},
	} {
		p, args := logPowerPlan(t, tc.name, types, Float8, tc.input)
		got, err := p.Evaluate(ctx, args, InputEnvironment{})
		if err != nil || !mathTrigMatches(got.floating, tc.want, 2) {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		name          string
		id            ID
		inputs        []string
		code, message string
	}{
		{"ln", Numeric, []string{"0"}, "2201E", "cannot take logarithm of zero"},
		{"sqrt", Numeric, []string{"-1"}, "2201F", "cannot take square root of a negative number"},
		{"log", Numeric, []string{"0", "-1"}, "2201E", "cannot take logarithm of zero"},
		{"log", Numeric, []string{"1", "2"}, "22012", "division by zero"},
		{"lgamma", Float8, []string{"-1"}, "22003", "value out of range: overflow"},
		{"width_bucket", Numeric, []string{"NaN", "0", "1", "5"}, "2201G", "operand, lower bound, and upper bound cannot be NaN"},
		{"width_bucket", Float8, []string{"NaN", "Infinity", "Infinity", "0"}, "2201G", "count must be greater than zero"},
		{"width_bucket", Numeric, []string{"1", "0", "1", "2147483647"}, "22003", "integer out of range"},
	} {
		p, args := logPowerPlan(t, tc.name, types, tc.id, tc.inputs...)
		got, err := p.Evaluate(ctx, args, InputEnvironment{})
		arraySearchUnitState(t, err, tc.code, tc.message)
		if got != (Value{}) {
			t.Fatal("error published datum", got)
		}
	}
}

func TestMathLogPowerSignatures(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name   string
		id     ID
		inputs []string
	}{
		{"lgamma", Float8, []string{"1"}}, {"ln", Float8, []string{"1"}}, {"ln", Numeric, []string{"1"}},
		{"log", Float8, []string{"100"}}, {"log", Numeric, []string{"100"}}, {"log", Numeric, []string{"2", "64"}},
		{"log10", Float8, []string{"100"}}, {"log10", Numeric, []string{"100"}},
		{"sqrt", Float8, []string{"4"}}, {"sqrt", Numeric, []string{"4"}},
		{"power", Float8, []string{"2", "3"}}, {"power", Numeric, []string{"2", "3"}},
		{"width_bucket", Float8, []string{"2", "0", "10", "5"}}, {"width_bucket", Numeric, []string{"2", "0", "10", "5"}},
	} {
		p, args := logPowerPlan(t, tc.name, types, tc.id, tc.inputs...)
		if p.ResultUsage() != p.result || p.Name() != tc.name {
			t.Fatal(p)
		}
		if got, err := p.Evaluate(context.Background(), args[:len(args)-1], InputEnvironment{}); err == nil || got != (Value{}) {
			t.Fatal("invalid arity", tc, got, err)
		}
		for i := range args {
			foreign := *args[i].typ
			saved := args[i]
			args[i] = Null(&foreign)
			if got, err := p.Evaluate(context.Background(), args, InputEnvironment{}); err == nil || got != (Value{}) {
				t.Fatal("foreign NULL owner", tc, i, got, err)
			}
			args[i] = saved
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		args[0] = Null(args[0].typ)
		if got, err := p.Evaluate(canceled, args, InputEnvironment{}); !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancel before strict NULL", tc, got, err)
		}
		bad := p.args[:p.arity]
		copyArgs := append([]Usage(nil), bad...)
		copyArgs[0].Typmod = 12
		if _, err := BindMath(tc.name, p.result, copyArgs...); err == nil {
			t.Fatal("argument typmod", tc)
		}
		result := p.result
		result.Typmod = 12
		if _, err := BindMath(tc.name, result, bad...); err == nil {
			t.Fatal("result typmod", tc)
		}
	}
	if _, err := BindMath("log", types[Float8].Usage(), types[Float8].Usage(), types[Float8].Usage()); err == nil {
		t.Fatal("binary float log admitted")
	}
	if _, err := BindMath("lgamma", types[Numeric].Usage(), types[Numeric].Usage()); err == nil {
		t.Fatal("numeric lgamma admitted")
	}
}

func TestMathLogPowerCancellation(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name   string
		inputs []string
	}{
		{"ln", []string{"1.000000000000000000000000000001"}}, {"sqrt", []string{"1e1000"}}, {"log", []string{"2", "1e-1000"}},
	} {
		p, args := logPowerPlan(t, tc.name, types, Numeric, tc.inputs...)
		original := append([]Value(nil), args...)
		for _, checkpoint := range []int{1, 2, 3} {
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
			got, err := p.Evaluate(ctx, args, InputEnvironment{})
			if !errors.Is(err, context.Canceled) || got != (Value{}) || !reflect.DeepEqual(original, args) {
				t.Fatal("checkpoint cancellation", tc, checkpoint, got, err)
			}
		}
		if _, err := p.Evaluate(context.Background(), args, InputEnvironment{}); err != nil {
			t.Fatal("reusable after cancel", tc, err)
		}
	}
	p, err := BindWidthBucketThreshold(types[Int4].Usage(), types[Int4].Usage(), types[1007].Usage(), CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"{0,1,2,3,4,5,6,7,8,9}", "{0,1,NULL,3,4,5,6,7,8,9}"} {
		args := []Value{arraySearchUnitInput(t, types[Int4], "5"), arraySearchUnitInput(t, types[1007], text)}
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 4}
		got, err := p.Evaluate(ctx, args, InputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("threshold scan cancellation", got, err)
		}
	}
	args := []Value{arraySearchUnitInput(t, types[Int4], "5"), arraySearchUnitInput(t, types[1007], "{0,1,2,3,4,5,6,7,8,9}")}
	for _, checkpoint := range []int{12, 13} {
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
		got, err := p.Evaluate(ctx, args, InputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("binary search cancellation", checkpoint, got, err)
		}
	}
	got, err := p.Evaluate(context.Background(), args, InputEnvironment{})
	if err != nil || got.integer != 6 {
		t.Fatal("threshold reuse after cancellation", got, err)
	}
}
