package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBitStringFunctionsBindingAndReownership(t *testing.T) {
	bit := func(text string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "b" + text}}
	}
	index := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}
	for _, tc := range []struct {
		name   string
		args   []parser.Expr
		result sqltype.ID
		text   string
		number int64
	}{
		{"bit_count", []parser.Expr{bit("10110")}, sqltype.Int8, "", 3},
		{"length", []parser.Expr{bit("10110")}, sqltype.Int4, "", 5},
		{"bit_length", []parser.Expr{bit("10110")}, sqltype.Int4, "", 5},
		{"octet_length", []parser.Expr{bit("10110")}, sqltype.Int4, "", 1},
		{"substring", []parser.Expr{bit("10110"), index}, sqltype.Bit, "0110", 0},
		{"overlay", []parser.Expr{bit("10110"), bit("00"), index}, sqltype.Bit, "10010", 0},
		{"position", []parser.Expr{bit("10110"), bit("11")}, sqltype.Int4, "", 3},
	} {
		for _, defaults := range []bool{false, true} {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			b.defaultConsumer = defaults
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: tc.args}
			var node BoundExpr
			var err error
			if tc.name == "substring" || tc.name == "bit_count" {
				node, err = b.bindBinaryFunction(raw, true)
			} else {
				node, err = b.bindStringFunction(raw, true)
			}
			if err != nil {
				t.Fatal(tc.name, defaults, err)
			}
			binary, ok := node.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
			if !ok || node.ResultUsage().Type.ID() != tc.result || node.ResultUsage().Typmod != -1 || node.ResultUsage().Collation != (sqltype.CollationRef{}) || node.Properties().Volatility != Immutable {
				t.Fatal(tc.name, node)
			}
			if err := binary.Validate(); err != nil {
				t.Fatal(err)
			}
			got, err := node.Eval(&EvalContext{Context: context.Background()})
			if err != nil {
				t.Fatal(err)
			}
			if tc.result == sqltype.Bit {
				text, err := got.Bits()
				if err != nil || text != tc.text {
					t.Fatal(tc.name, text, err)
				}
			} else {
				want, _ := sqltype.Integer(node.ResultUsage().Type, tc.number)
				if got != want {
					t.Fatal(tc.name, got, want)
				}
			}
			replacements := map[sqltype.ID]*sqltype.Descriptor{}
			for _, d := range sqltype.Builtins() {
				replacements[d.ID()] = d
			}
			rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
			if err != nil || !changed {
				t.Fatal(tc.name, changed, err)
			}
			next := rebound.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
			if err := next.Validate(); err != nil {
				t.Fatal(err)
			}
			if next.Plan == binary.Plan {
				t.Fatal("provider retained old owner")
			}
			after, err := next.Eval(&EvalContext{Context: context.Background()})
			want, retagErr := got.Retag(replacements[tc.result])
			if err != nil || retagErr != nil || after != want {
				t.Fatal(tc.name, after, want, err)
			}
			// Ordinary and SQL-special position orders are intentionally different.
			if tc.name == "position" {
				ordinary, err := renderBinaryCall(tc.name, parser.OrdinaryFunctionSyntax, []string{"source", "needle"}, "pg_catalog.position", func(s string) (string, error) { return s, nil })
				special, specialErr := renderBinaryCall(tc.name, parser.SQLFunctionSyntax, []string{"source", "needle"}, "", func(s string) (string, error) { return s, nil })
				if err != nil || specialErr != nil || ordinary != "pg_catalog.position(source, needle)" || special != "POSITION((needle) IN (source))" {
					t.Fatal(ordinary, special, err, specialErr)
				}
			}
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "substring"}}}, Args: []parser.Expr{bit("10110"), parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}}}
	node, err := b.bindBinaryFunction(raw, true)
	if err != nil || !node.Properties().ParameterDependent || b.parameters[1].Type.ID() != sqltype.Int4 || b.parameters[2].Type.ID() != sqltype.Int4 {
		t.Fatal(node, err, b.parameters)
	}
	raw.Name.Parts[0].Text = "substr"
	if _, err := b.bindBinaryFunction(raw, true); err == nil {
		t.Fatal("substr bit admitted")
	}
	// BYTEA defaults retain their preexisting capability boundary.
	b.defaultConsumer = true
	raw.Name.Parts[0].Text = "length"
	raw.Args = []parser.Expr{parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x01"}}, TypeName: parser.TypeName{Name: "bytea"}}}
	_, err = b.bindStringFunction(raw, true)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "0A000" || !strings.Contains(err.Error(), "functions are unsupported") {
		t.Fatal(err)
	}
}

func TestBitStringFunctionsBoundedOperands(t *testing.T) {
	for _, name := range []string{"bit_count", "length", "bit_length", "octet_length", "substring", "overlay", "position"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		sourceUsage := b.typ(sqltype.Bit).Usage()
		sourceUsage.Typmod = 3
		sourceValue, err := sqltype.BitString(sourceUsage.Type, "101")
		if err != nil {
			t.Fatal(err)
		}
		source := literalExpression{expressionInfo: expressionInfo{Usage: sourceUsage}, value: sourceValue}
		operands := []functions.Argument[BoundExpr]{{Expression: source, Usage: sourceUsage}}
		if name == "overlay" || name == "position" {
			operands = append(operands, operands[0])
		}
		if name == "overlay" || name == "substring" {
			usage := b.typ(sqltype.Int4).Usage()
			value, err := sqltype.Integer(usage.Type, 2)
			if err != nil {
				t.Fatal(err)
			}
			index := literalExpression{expressionInfo: expressionInfo{Usage: usage}, value: value}
			operands = append(operands, functions.Argument[BoundExpr]{Expression: index, Usage: usage})
		}
		var types functions.BinaryTypes
		node, err := b.finishBinary(name, sqltype.Bit, parser.Position{}, operands, parser.OrdinaryFunctionSyntax, &types)
		if err != nil {
			t.Fatal(name, err)
		}
		binary := node.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
		if err := binary.Validate(); err != nil {
			t.Fatal(name, err)
		}
		for _, arg := range binary.Args {
			if arg.ResultUsage().Typmod != -1 {
				t.Fatal(name, "bounded formal", arg.ResultUsage())
			}
		}
		if source.ResultUsage() != sourceUsage || node.ResultUsage().Typmod != -1 {
			t.Fatal(name, "source or result width changed")
		}
		if _, err := node.Eval(&EvalContext{Context: context.Background()}); err != nil {
			t.Fatal(name, err)
		}
	}
}
