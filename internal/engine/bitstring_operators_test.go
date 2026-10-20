package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func bitOperatorRaw(name string, args ...parser.Expr) parser.OperatorExpr {
	return parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
}
func bitOperatorLiteral(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "b" + text}}
}

func TestBitStringOperatorsBindingAndReownership(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ name, want string }{
		{"&", "00001"}, {"|", "11101"}, {"#", "11100"}, {"~", "01110"}, {"<<", "01000"}, {">>", "00010"}, {"||", "1000101101"},
	} {
		e := New()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		args := []parser.Expr{bitOperatorLiteral("10001"), bitOperatorLiteral("01101")}
		if tc.name == "~" {
			args = args[:1]
		}
		if tc.name == "<<" || tc.name == ">>" {
			args[1] = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 3}}
		}
		raw := bitOperatorRaw(tc.name, args...)
		var node expr.Expr
		var err error
		if tc.name == "||" {
			node, err = b.bindStringConcat(raw)
		} else {
			node, err = b.bindBitwise(raw)
		}
		if err != nil {
			t.Fatal(tc.name, err)
		}
		id := sqltype.Bit
		if tc.name == "||" {
			id = sqltype.VarBit
		}
		if node.ResultUsage() != b.typ(id).Usage() || node.Properties() != (expr.Properties{}) {
			t.Fatal(tc.name, node.ResultUsage(), node.Properties())
		}
		got, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		text, textErr := got.Bits()
		if err != nil || textErr != nil || text != tc.want {
			t.Fatal(tc.name, text, err, textErr)
		}
		serialized, err := defaultSQL(ctx, e.CatalogRead(), node)
		if err != nil || !strings.Contains(serialized, tc.name) || strings.Contains(serialized, "CAST(") {
			t.Fatal(tc.name, serialized, err)
		}
		if !b.sameGroupExpression(node, node) {
			t.Fatal("structural equality rejected self")
		}
		replacement := New()
		rebound, changed, err := retagBoundTypes(replacement.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			mapped, _ := replacement.CatalogRead().LookupType(d.ID())
			return mapped
		})
		if err != nil || !changed || rebound.ResultUsage().Type == node.ResultUsage().Type {
			t.Fatal(tc.name, changed, err)
		}
		after, err := rebound.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		want, retagErr := got.Retag(rebound.ResultUsage().Type)
		if err != nil || retagErr != nil || after != want {
			t.Fatal(tc.name, after, want, err, retagErr)
		}
		if _, err := defaultSQL(ctx, replacement.CatalogRead(), rebound); err != nil {
			t.Fatal(tc.name, err)
		}
		if tc.name != "||" {
			original := node.(plan.BitwiseExpr)
			next := rebound.(plan.BitwiseExpr)
			if original.Bits == (sqltype.BitStringBitwise{}) || original.Provider != (sqltype.Bitwise{}) || original.Bits == next.Bits {
				t.Fatal("wrong bit provider ownership")
			}
			corrupt := original
			corrupt.Bits = next.Bits
			if b.sameGroupExpression(original, corrupt) {
				t.Fatal("structural equality ignored bit provider")
			}
		} else {
			binary := rebound.(functions.Binary)
			if err := binary.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBitStringOperatorsStoredValidation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	node, err := b.bindBitwise(bitOperatorRaw("&", bitOperatorLiteral("10001"), bitOperatorLiteral("01101")))
	if err != nil {
		t.Fatal(err)
	}
	root := node.(plan.BitwiseExpr)
	integral, err := sqltype.BindBitwise(sqltype.BitwiseAnd, b.typ(sqltype.Int4), b.typ(sqltype.Int4))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(plan.BitwiseExpr) plan.BitwiseExpr{
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Bits = sqltype.BitStringBitwise{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Provider = integral; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Left = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOperator(255); return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Usage.Typmod = 3; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			n.Right = n.Left.(plan.LiteralExpr)
			r := n.Right.(plan.LiteralExpr)
			r.Usage.Typmod = 3
			n.Right = r
			return n
		},
	} {
		if _, err := defaultSQL(ctx, e.CatalogRead(), mutate(root)); err == nil {
			t.Fatal("invalid bit stored provider admitted")
		}
	}
	// Signature selection remains analysis-only; width failures belong to the
	// Execute preflight even if a later filter would discard every result.
	bad, err := b.bindBitwise(bitOperatorRaw("&", bitOperatorLiteral("1"), bitOperatorLiteral("00")))
	if err != nil {
		t.Fatal("width checked during analysis", err)
	}
	_, _, err = preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: ctx}}, bad)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22026" || d.Message != "cannot AND bit strings of different sizes" || d.Position != "" {
		t.Fatal(err)
	}
	folded, changed, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: ctx}}, root)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if _, ok := folded.(plan.LiteralExpr); !ok {
		t.Fatal("constant bit operator not folded")
	}
	got, err := folded.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
	text, _ := got.Bits()
	if err != nil || text != "00001" {
		t.Fatal(got, err)
	}
	// An imported parameter literal must not acquire immutable-constant demand.
	dependent := root
	dependent.Property.ParameterDependent = true
	left := dependent.Left.(plan.LiteralExpr)
	left.Property.ParameterDependent = true
	dependent.Left = left
	if _, changed, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: ctx}}, dependent); err != nil || changed {
		t.Fatal("parameter-dependent bit expression folded", changed, err)
	}
}

func TestBitStringOperatorsOperandEvaluation(t *testing.T) {
	ctx := context.Background()
	e := New()
	bit, _ := e.CatalogRead().LookupType(sqltype.Bit)
	provider, err := sqltype.BindBitStringBitwise(sqltype.BitwiseAnd, bit, bit)
	if err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		var calls []string
		null := queryProbe(bit.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls = append(calls, "null")
			return sqltype.Null(bit), nil
		})
		failure := queryProbe(bit.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls = append(calls, "error")
			return sqltype.Value{}, &sqlstate.Error{Code: "22026", Message: "cannot AND bit strings of different sizes"}
		})
		root := plan.BitwiseExpr{Info: expr.Info{Usage: bit.Usage()}, Left: null, Right: failure, Bits: provider, Operator: sqltype.BitwiseAnd}
		if reverse {
			root.Left, root.Right = root.Right, root.Left
		}
		_, err := root.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22026" {
			t.Fatal(err)
		}
		if !reverse && (len(calls) != 2 || calls[0] != "null" || calls[1] != "error") || reverse && (len(calls) != 1 || calls[0] != "error") {
			t.Fatal("operands reordered or repeated", calls)
		}
	}
}

func TestBitStringOperatorsBoundedConcatArguments(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Bit, sqltype.VarBit} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		usage := b.typ(id).Usage()
		usage.Typmod = 3
		value, err := sqltype.BitString(usage.Type, "101")
		if err != nil {
			t.Fatal(err)
		}
		source := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}
		operands := []functions.Argument{{Expression: source, Usage: usage}, {Expression: source, Usage: usage}}
		var types functions.BinaryTypes
		node, err := functions.FinishBinary(b, "||", sqltype.VarBit, parser.Position{}, operands, parser.OrdinaryFunctionSyntax, &types)
		if err != nil {
			t.Fatal(id, err)
		}
		binary := node.(functions.Binary)
		if err := binary.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, arg := range binary.Args {
			if arg.ResultUsage() != b.typ(sqltype.VarBit).Usage() {
				t.Fatal("bounded concat formal", arg.ResultUsage())
			}
		}
		if source.ResultUsage() != usage {
			t.Fatal("source width mutated")
		}
		got, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
		text, _ := got.Bits()
		if err != nil || text != "101101" {
			t.Fatal(got, err)
		}
	}
}
