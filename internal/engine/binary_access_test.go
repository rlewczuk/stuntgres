package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
		{"get_bit", []sqltype.ID{0, sqltype.Int4}, "", sqltype.Int4},
		{"get_bit", []sqltype.ID{0, sqltype.Int2}, "42725", 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int8}, "", sqltype.Int4},
		{"get_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int8}, "42883", 0},
		{"set_bit", []sqltype.ID{sqltype.Bytea, sqltype.Int4, sqltype.Int8}, "42883", 0},
		{"set_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int2, sqltype.Int2}, "", sqltype.Bytea},
		{"get_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4}, "", sqltype.Int4},
		{"get_bit", []sqltype.ID{sqltype.VarBit, sqltype.Int2}, "", sqltype.Int4},
		{"get_bit", []sqltype.ID{sqltype.Bit, sqltype.Int8}, "42883", 0},
		{"set_bit", []sqltype.ID{0, sqltype.Int4, sqltype.Int4}, "", sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4, sqltype.Int4}, "", sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.VarBit, sqltype.Int2, sqltype.Int2}, "", sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4, sqltype.Int8}, "42883", 0},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		args := make([]parser.Expr, len(tc.ids))
		for i, id := range tc.ids {
			null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
			args[i] = null
			if id != 0 {
				// Supply typed operands directly: overload resolution does not
				// require (or admit) general BIT/VARBIT SQL casts.
				b.Params.Parameters[i+1] = b.Type(id).Usage()
				args[i] = parser.ParameterExpr{Number: i + 1}
			}
		}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Pos: parser.Position{Character: 8}, Args: args}
		got, err := functions.BindBinary(b, raw, true)
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
	bitAccessBindingProperties(t)
}

func TestBinaryAccessRetagAndRestoreArguments(t *testing.T) {
	types, replacement := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		replacement[d.ID()] = d
	}
	for _, tc := range []struct {
		name   string
		family sqltype.ID
	}{
		{"bit_count", sqltype.Bytea}, {"get_bit", sqltype.Bytea},
		{"get_byte", sqltype.Bytea}, {"set_bit", sqltype.Bytea},
		{"set_byte", sqltype.Bytea}, {"get_bit", sqltype.Bit}, {"set_bit", sqltype.Bit},
	} {
		name := tc.name
		arity := 2
		result := sqltype.Int4
		if name == "bit_count" {
			arity, result = 1, sqltype.Int8
		}
		if name == "set_bit" || name == "set_byte" {
			arity, result = 3, tc.family
		}
		args := make([]expr.Expr, arity)
		usages := make([]sqltype.Usage, arity)
		for i := range args {
			d := types[functions.BinaryArgumentID(name, i, tc.family)]
			usages[i] = d.Usage()
			value := sqltype.Null(d)
			if i == 0 {
				if tc.family == sqltype.Bit {
					value, _ = sqltype.BitString(d, "100000001")
				} else {
					value, _ = sqltype.Binary(d, "\x01\x80")
				}
			} else {
				value, _ = sqltype.Integer(d, 0)
			}
			args[i] = plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
		}
		p, err := sqltype.BindBinary(name, types[result].Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		original := functions.Binary{Info: expr.Info{Usage: types[result].Usage()}, Plan: p, Args: args}
		rebound, changed, err := stored.RetagTypes(New().CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(name, changed, err)
		}
		next := rebound.(functions.Binary)
		got, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		before, oldErr := original.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
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
			if err := dump.RestoreScalar(raw, false); err == nil {
				t.Fatal(name, slot, "Restore accepted parameter")
			}
		}
	}
}

func bitAccessBindingProperties(t *testing.T) {
	t.Helper()
	for _, name := range []string{"get_bit", "set_bit"} {
		for _, unknown := range []bool{false, true} {
			for _, defaults := range []bool{false, true} {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				b.Consumer.DefaultConsumer = defaults
				source := parser.Value{Kind: parser.BitStringValue, Text: "b101"}
				if unknown {
					source = parser.Value{Kind: parser.StringValue, Text: "101"}
				}
				args := []parser.Expr{parser.LiteralExpr{Value: source}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}}}
				if name == "set_bit" {
					args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
				}
				raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
				node, err := functions.BindBinary(b, raw, true)
				if err != nil {
					t.Fatal(name, unknown, defaults, err)
				}
				binary := node.(functions.Binary)
				if err := binary.Validate(); err != nil {
					t.Fatal(err)
				}
				if node.Properties().Volatility != expr.Immutable || node.ResultUsage().Typmod != -1 || binary.Args[0].ResultUsage().Type.ID() != sqltype.Bit || binary.Args[1].ResultUsage().Type.ID() != sqltype.Int4 {
					t.Fatal(name, node)
				}
				// DEFAULT admission must not widen the adjacent BYTEA family.
				b.Consumer.DefaultConsumer = true
				raw.Args[0] = parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x80"}}, TypeName: parser.TypeName{Name: "bytea"}}
				_, err = functions.BindBinary(b, raw, true)
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "0A000" {
					t.Fatal("BYTEA default boundary", name, err)
				}
			}
		}
		// The index is INT4 even when a parameter has no independent type.
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{
			parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "b101"}},
			parser.ParameterExpr{Number: 1},
		}}
		if name == "set_bit" {
			raw.Args = append(raw.Args, parser.ParameterExpr{Number: 2})
		}
		node, err := functions.BindBinary(b, raw, true)
		if err != nil || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable || b.Params.Parameters[1].Type.ID() != sqltype.Int4 {
			t.Fatal("parameter propagation", name, node, err)
		}
		for _, family := range []sqltype.ID{sqltype.Bit, sqltype.VarBit} {
			usage := b.Type(family).Usage()
			usage.Typmod = 3
			value, err := sqltype.BitString(usage.Type, "101")
			if err != nil {
				t.Fatal(err)
			}
			props := expr.Properties{Volatility: expr.Stable, RowDependent: true, StageDependent: true, DomainDependent: true}
			pos := parser.Position{Character: 17}
			source := plan.LiteralExpr{Info: expr.Info{Usage: usage, Pos: pos, Property: props}, Value: value}
			operands := []functions.Argument{{Expression: source, Usage: usage}}
			for i := 1; i < len(raw.Args); i++ {
				u := b.Type(sqltype.Int4).Usage()
				v, _ := sqltype.Integer(u.Type, 0)
				operands = append(operands, functions.Argument{Expression: plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: v}, Usage: u})
			}
			var types functions.BinaryTypes
			node, err := functions.FinishBinary(b, name, sqltype.Bit, parser.Position{}, operands, parser.OrdinaryFunctionSyntax, &types)
			if err != nil {
				t.Fatal(name, family, err)
			}
			binary := node.(functions.Binary)
			if err := binary.Validate(); err != nil {
				t.Fatal(err)
			}
			if node.Properties() != props || node.ResultUsage().Typmod != -1 || source.ResultUsage() != usage || binary.Args[0].ResultUsage().Typmod != -1 || binary.Args[0].SourcePosition() != pos {
				t.Fatal("width/dependency/source propagation", name, family, node.Properties())
			}
		}
	}
}
