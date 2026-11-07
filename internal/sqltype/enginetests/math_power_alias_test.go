package enginetests

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func mathPowerAliasPlan(t *testing.T, name string, typ *sqltype.Descriptor, inputs [2]string) (sqltype.MathPlan, [2]sqltype.Value) {
	t.Helper()
	plan, err := sqltype.BindMath(name, typ.Usage(), typ.Usage(), typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	var values [2]sqltype.Value
	for i, input := range inputs {
		values[i], err = typ.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
	}
	return plan, values
}

func mathPowerAliasTypes() []*sqltype.Descriptor {
	var types []*sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Numeric || typ.ID() == sqltype.Float8 {
			types = append(types, typ)
		}
	}
	return types
}

func TestMathLogPowerPowProvider(t *testing.T) {
	ctx := context.Background()
	for _, typ := range mathPowerAliasTypes() {
		inputs, want := [2]string{"2", "3"}, "8"
		if typ.ID() == sqltype.Numeric {
			inputs, want = [2]string{"9007199254740993", "2"}, "81129638414606699710187514626049"
		}
		for _, name := range []string{"pow", "power"} {
			plan, values := mathPowerAliasPlan(t, name, typ, inputs)
			original := values
			if plan.Name() != name || plan.ResultUsage() != typ.Usage() {
				t.Fatal("lost original name or result owner", name, plan)
			}
			got, err := plan.Evaluate(ctx, values[:], sqltype.InputEnvironment{})
			if err != nil || got.Type() != typ || got.IsNull() {
				t.Fatal(name, typ.ID(), got, err)
			}
			text, err := typ.Output(ctx, got)
			if err != nil || text != want || values != original {
				t.Fatal("exact result or immutable operands", name, typ.ID(), text, err)
			}
			for i, input := range inputs {
				text, err := typ.Output(ctx, values[i])
				if err != nil || text != input {
					t.Fatal("input backing changed", name, i, text, err)
				}
				values[i] = sqltype.Null(typ)
				got, err := plan.Evaluate(ctx, values[:], sqltype.InputEnvironment{})
				if err != nil || got != sqltype.Null(typ) {
					t.Fatal("result-owned strict NULL", name, i, got, err)
				}
				values[i] = original[i]
			}
		}
	}
}

func TestMathLogPowerPowSignatures(t *testing.T) {
	ctx := context.Background()
	for _, typ := range mathPowerAliasTypes() {
		foreign := *typ
		for _, name := range []string{"pow", "power"} {
			plan, values := mathPowerAliasPlan(t, name, typ, [2]string{"2", "3"})
			original := values
			if _, err := sqltype.BindMath(name, foreign.Usage(), typ.Usage(), typ.Usage()); err == nil {
				t.Fatal("foreign result owner admitted", name, typ.ID())
			}
			if _, err := sqltype.BindMath(name, typ.Usage(), typ.Usage(), foreign.Usage()); err == nil {
				t.Fatal("mixed formal owners admitted", name, typ.ID())
			}
			if got, err := plan.Evaluate(ctx, values[:1], sqltype.InputEnvironment{}); err == nil || got != (sqltype.Value{}) {
				t.Fatal("invalid value arity admitted", name, got, err)
			}
			for i := range values {
				foreignValue, err := foreign.Input(ctx, "2", -1)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []sqltype.Value{foreignValue, sqltype.Null(&foreign)} {
					values[i] = value
					if got, err := plan.Evaluate(ctx, values[:], sqltype.InputEnvironment{}); err == nil || got != (sqltype.Value{}) {
						t.Fatal("foreign value owner admitted", name, i, value.IsNull(), got, err)
					}
				}
				values[i] = original[i]
			}
			if got, err := plan.Evaluate(ctx, values[:], sqltype.InputEnvironment{}); err != nil || got.Type() != typ || got.IsNull() || values != original {
				t.Fatal("owner rejection poisoned reusable plan", name, got, err)
			}
		}
	}
}

func TestMathLogPowerPowCancellation(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, typ := range mathPowerAliasTypes() {
		for _, name := range []string{"pow", "power"} {
			plan, values := mathPowerAliasPlan(t, name, typ, [2]string{"2", "3"})
			original := values
			for i := range values {
				values[i] = sqltype.Null(typ)
				before := values
				got, err := plan.Evaluate(canceled, values[:], sqltype.InputEnvironment{})
				if !errors.Is(err, context.Canceled) || got != (sqltype.Value{}) || values != before {
					t.Fatal("cancellation must precede strict NULL without mutation", name, typ.ID(), i, got, err)
				}
				values[i] = original[i]
			}
			got, err := plan.Evaluate(ctx, values[:], sqltype.InputEnvironment{})
			if err != nil || got.Type() != typ || got.IsNull() || values != original {
				t.Fatal("cancellation poisoned reusable plan", name, typ.ID(), got, err)
			}
			want := "8"
			if typ.ID() == sqltype.Numeric {
				want = "8.0000000000000000"
			}
			text, err := typ.Output(ctx, got)
			if err != nil || text != want {
				t.Fatal("reusable plan result", name, typ.ID(), text, err)
			}
		}
	}
}
