package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryAccessResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ids    []sqltype.ID
		code   string
		result sqltype.ID
	}{
		{"bit_count", []sqltype.ID{sqltype.Bytea}, "", sqltype.Int8},
		{"bit_count", []sqltype.ID{0}, "42725", 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int4}, "0A000", 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int2}, "42725", 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int8}, "", sqltype.Int4},
		{"get_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int8}, "42883", 0},
		{"set_bit", []sqltype.ID{sqltype.Bytea, sqltype.Int4, sqltype.Int8}, "42883", 0},
		{"set_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int2, sqltype.Int2}, "", sqltype.Bytea},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		operands := make([]comparisonOperand, len(tc.ids))
		names := make([]string, len(tc.ids))
		for i, id := range tc.ids {
			names[i] = "unknown"
			if id == 0 {
				operands[i] = comparisonOperand{raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
				continue
			}
			d := b.typ(id)
			value := sqltype.Null(d)
			operands[i] = comparisonOperand{node: literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: value}}
			names[i] = d.DiagnosticName()
		}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Pos: parser.Position{Character: 8}, Args: make([]parser.Expr, len(tc.ids))}
		got, err := b.bindBinaryAccess(raw, true, operands, names)
		if tc.code != "" {
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != tc.code {
				t.Fatal(tc, got, err)
			}
			continue
		}
		if err != nil || got.ResultUsage().Type.ID() != tc.result {
			t.Fatal(tc, got, err)
		}
	}
}

func TestBinaryAccessRetagAndRestoreArguments(t *testing.T) {
	types, replacement := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		replacement[d.ID()] = d
	}
	for _, name := range []string{"bit_count", "get_bit", "get_byte", "set_bit", "set_byte"} {
		arity := 2
		result := sqltype.Int4
		if name == "bit_count" {
			arity, result = 1, sqltype.Int8
		}
		if name == "set_bit" || name == "set_byte" {
			arity, result = 3, sqltype.Bytea
		}
		args := make([]BoundExpr, arity)
		usages := make([]sqltype.Usage, arity)
		for i := range args {
			d := types[binaryArgumentID(name, i)]
			usages[i] = d.Usage()
			value := sqltype.Null(d)
			if i == 0 {
				value, _ = sqltype.Binary(d, "\x01\x80")
			} else {
				value, _ = sqltype.Integer(d, 0)
			}
			args[i] = literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: value}
		}
		p, err := sqltype.BindBinary(name, types[result].Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		original := binaryExpression{expressionInfo: expressionInfo{usage: types[result].Usage()}, plan: p, args: args}
		rebound, changed, err := retagBoundTypes(original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(name, changed, err)
		}
		next := rebound.(binaryExpression)
		got, err := next.Eval(&EvalContext{Context: context.Background()})
		before, oldErr := original.Eval(&EvalContext{Context: context.Background()})
		want, retagErr := before.Retag(replacement[result])
		if err != nil || oldErr != nil || retagErr != nil || got != want {
			t.Fatal(name, got, want, err)
		}
		for slot := range arity {
			rawArgs := make([]parser.Expr, arity)
			for i := range rawArgs {
				rawArgs[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
			}
			rawArgs[slot] = parser.ParameterExpr{Number: 1}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: rawArgs}
			if err := restoreScalar(raw); err == nil {
				t.Fatal(name, slot, "Restore accepted parameter")
			}
		}
	}
}
