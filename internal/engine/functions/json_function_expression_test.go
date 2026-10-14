package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestJSONFunctionKeysScalarAdmission(t *testing.T) {
	var text, json, jsonb *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.JSON:
			json = d
		case sqltype.JSONB:
			jsonb = d
		}
	}
	for _, input := range []*sqltype.Descriptor{json, jsonb} {
		name := "json_object_keys"
		if input == jsonb {
			name = "jsonb_object_keys"
		}
		value, err := input.Input(context.Background(), `{}`, -1)
		if err != nil {
			t.Fatal(err)
		}
		argument := functionTestArgument{Info: Info[struct{}]{Usage: input.Usage()}, value: value, literal: true}
		marker, err := NewJSONKeys[functionTestExpression, *functionTestContext](Info[struct{}]{Usage: text.Usage()}, name, argument)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := marker.Eval(&functionTestContext{context.Background()}); err == nil {
			t.Fatal("set ran as scalar")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := marker.Eval(&functionTestContext{ctx}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestJSONFunctionScalarRuntimeNull(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		name          string
		input, result sqltype.ID
	}{
		{"json_typeof", sqltype.JSON, sqltype.Text},
		{"jsonb_typeof", sqltype.JSONB, sqltype.Text},
		{"json_array_length", sqltype.JSON, sqltype.Int4},
		{"jsonb_array_length", sqltype.JSONB, sqltype.Int4},
	} {
		source, result := types[tc.input], types[tc.result]
		plan, err := sqltype.BindBinary(tc.name, result.Usage(), source.Usage())
		if err != nil {
			t.Fatal(err)
		}
		var order []int
		call := Binary[binaryUnitExpression, *binaryUnitContext, struct{}]{Info: Info[struct{}]{Usage: result.Usage()}, Plan: plan, Args: []binaryUnitExpression{
			binaryUnitArgument{Info: Info[struct{}]{Usage: source.Usage()}, value: sqltype.Null(source), order: &order, index: 0},
		}}
		value, err := call.Eval(&binaryUnitContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != call.ResultUsage().Type || len(order) != 1 || order[0] != 0 {
			t.Fatal(value, order, err)
		}
	}
}
