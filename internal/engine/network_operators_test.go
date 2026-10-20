package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func networkOperatorTyped(typ, text string) parser.Expr {
	return parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: typ}, Text: text}
}

func TestNetworkOperatorBindingAndReownership(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, want string
		args       []parser.Expr
		result     sqltype.ID
	}{
		{"~", "63.87.254.249/24", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24")}, sqltype.Inet},
		{"&", "0.0.0.6/25", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24"), networkOperatorTyped("inet", "0.0.0.255/25")}, sqltype.Inet},
		{"|", "192.168.1.255/25", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24"), networkOperatorTyped("inet", "0.0.0.255/25")}, sqltype.Inet},
		{"~", "63.87.254.255/24", []parser.Expr{networkOperatorTyped("cidr", "192.168.1/24")}, sqltype.Inet},
		{"+", "192.168.1.7/24", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24"), parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}, sqltype.Inet},
		{"+", "192.168.1.7/24", []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, networkOperatorTyped("inet", "192.168.1.6/24")}, sqltype.Inet},
		{"-", "192.168.1.5/24", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24"), parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}, sqltype.Inet},
		{"-", "5", []parser.Expr{networkOperatorTyped("inet", "192.168.1.6/24"), networkOperatorTyped("inet", "192.168.1.1")}, sqltype.Int8},
	} {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			raw := bitOperatorRaw(tc.name, tc.args...)
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if node.ResultUsage() != b.typ(tc.result).Usage() || node.Properties() != (expr.Properties{}) {
				t.Fatal(node.ResultUsage(), node.Properties())
			}
			value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			text, err := value.Type().Output(ctx, value)
			if err != nil || text != tc.want {
				t.Fatal(text, err)
			}
			serialized, err := defaultSQL(ctx, e.CatalogRead(), node)
			if err != nil || !strings.Contains(serialized, tc.name) {
				t.Fatal(serialized, err)
			}
			if !b.sameGroupExpression(node, node) {
				t.Fatal("self structural equality")
			}
			replacement := New()
			rebound, changed, err := retagBoundTypes(replacement.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
				next, _ := replacement.CatalogRead().LookupType(d.ID())
				return next
			})
			if err != nil || !changed || rebound.ResultUsage().Type == node.ResultUsage().Type {
				t.Fatal(changed, err)
			}
			after, err := rebound.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			want, err := value.Retag(rebound.ResultUsage().Type)
			if err != nil || after != want {
				t.Fatal(after, want, err)
			}
			if _, err := defaultSQL(ctx, replacement.CatalogRead(), rebound); err != nil {
				t.Fatal(err)
			}
			if original, ok := node.(plan.BitwiseExpr); ok {
				next := rebound.(plan.BitwiseExpr)
				if original.Network == (sqltype.NetworkBitwise{}) || original.Provider != (sqltype.Bitwise{}) || original.Bits != (sqltype.BitStringBitwise{}) || original.Network == next.Network {
					t.Fatal("network provider ownership")
				}
				corrupt := original
				corrupt.Network = next.Network
				if b.sameGroupExpression(original, corrupt) {
					t.Fatal("network provider omitted from structural equality")
				}
			}
		})
	}
	// Exactly-once left-to-right child evaluation remains unchanged even for a
	// strict NULL; a child's error is not the operator's suppressed family check.
	e := New()
	inet, _ := e.CatalogRead().LookupType(sqltype.Inet)
	p, err := sqltype.BindNetworkBitwise(sqltype.BitwiseAnd, inet, inet)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	null := queryProbe(inet.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls = append(calls, "left")
		return sqltype.Null(inet), nil
	})
	failure := errors.New("network child failure")
	right := queryProbe(inet.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls = append(calls, "right")
		return sqltype.Value{}, failure
	})
	node := plan.BitwiseExpr{Info: expr.Info{Usage: inet.Usage()}, Left: null, Right: right, Network: p, Operator: sqltype.BitwiseAnd}
	if _, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}}); !errors.Is(err, failure) || !reflect.DeepEqual(calls, []string{"left", "right"}) {
		t.Fatal(calls, err)
	}
}

func TestNetworkOperatorStoredValidation(t *testing.T) {
	t.Run("arithmetic-recipes", networkOperatorArithmeticStoredValidation)
	t.Run("optional-preflight-children", networkOperatorOptionalPreflightChildren)
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	node, err := b.bindBitwise(bitOperatorRaw("&", networkOperatorTyped("inet", "192.168.1.6/24"), networkOperatorTyped("inet", "0.0.0.255/25")))
	if err != nil {
		t.Fatal(err)
	}
	root := node.(plan.BitwiseExpr)
	integral, err := sqltype.BindBitwise(sqltype.BitwiseAnd, b.typ(sqltype.Int4), b.typ(sqltype.Int4))
	if err != nil {
		t.Fatal(err)
	}
	bits, err := sqltype.BindBitStringBitwise(sqltype.BitwiseAnd, b.typ(sqltype.Bit), b.typ(sqltype.Bit))
	if err != nil {
		t.Fatal(err)
	}
	foreign := New()
	foreignInet, _ := foreign.CatalogRead().LookupType(sqltype.Inet)
	foreignProvider, err := sqltype.BindNetworkBitwise(sqltype.BitwiseAnd, foreignInet, foreignInet)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(plan.BitwiseExpr) plan.BitwiseExpr{
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Network = sqltype.NetworkBitwise{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Network = foreignProvider; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Provider = integral; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Bits = bits; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Left = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOr; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOperator(255); return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Usage.Typmod = 4; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Usage.Type = b.typ(sqltype.Cidr); return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			v, err := foreignInet.Input(ctx, "::1", -1)
			if err != nil {
				t.Fatal(err)
			}
			n.Right = plan.LiteralExpr{Info: expr.Info{Usage: foreignInet.Usage()}, Value: v}
			return n
		},
	} {
		if _, err := defaultSQL(ctx, e.CatalogRead(), mutate(root)); err == nil {
			t.Fatal("invalid network recipe accepted")
		}
	}
	// The inactive network field must also be rejected for existing providers.
	for _, raw := range []parser.OperatorExpr{
		bitOperatorRaw("&", bitOperatorLiteral("1"), bitOperatorLiteral("1")),
		bitOperatorRaw("&", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}),
	} {
		other, err := b.bindBitwise(raw)
		if err != nil {
			t.Fatal(err)
		}
		corrupt := other.(plan.BitwiseExpr)
		corrupt.Network = root.Network
		if _, err := defaultSQL(ctx, e.CatalogRead(), corrupt); err == nil {
			t.Fatal("inactive network provider accepted")
		}
	}
	// Import-time preflight is scoped to network signatures and preserves the
	// original parameter dependence even after producing an execution literal.
	inet, int8 := b.typ(sqltype.Inet), b.typ(sqltype.Int8)
	v4, err := inet.Input(ctx, "1.2.3.4", -1)
	if err != nil {
		t.Fatal(err)
	}
	v6, err := inet.Input(ctx, "::1", -1)
	if err != nil {
		t.Fatal(err)
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: inet.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	family := root
	family.Left = parameter
	family.Right = plan.LiteralExpr{Info: expr.Info{Usage: inet.Usage()}, Value: v6}
	family.Property = parameter.Property
	for _, frame := range [][]sqltype.Value{{v4}, {v6}, {sqltype.Null(inet)}, {v4}} {
		c := &EvalContext{EvalStatement: evalStatement{Context: ctx, Parameters: frame}}
		folded, changed, err := preflightCoercions(c, family)
		if frame[0] == v4 {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22023" || d.Position != "" {
				t.Fatal(err)
			}
		} else {
			if err != nil || !changed || !folded.Properties().ParameterDependent {
				t.Fatal(changed, err)
			}
			value, err := folded.Eval(c)
			if err != nil || value.IsNull() != frame[0].IsNull() {
				t.Fatal(value, err)
			}
		}
	}
	for _, barrier := range []expr.Properties{{RowDependent: true}, {StageDependent: true}, {DomainDependent: true}, {RuntimeDomainConversion: true}, {Volatility: expr.Stable}, {Volatility: expr.Volatile}} {
		lazy := family
		lazy.Property = expr.MergeProperties(lazy.Property, barrier)
		if _, changed, err := preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: ctx, Parameters: []sqltype.Value{v4}}}, lazy); err != nil || changed {
			t.Fatal("network preflight crossed barrier", barrier, changed, err)
		}
	}
	// Address subtraction returns INT8: network detection uses input signature,
	// never just the result type. Offset and commuted addition share the gate.
	one, err := sqltype.Integer(int8, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op            sqltype.ArithmeticOperator
		left, right   expr.Expr
		result        *sqltype.Descriptor
		frame         []sqltype.Value
		code, context string
	}{
		{sqltype.ArithmeticSubtract, parameter, plan.LiteralExpr{Info: expr.Info{Usage: inet.Usage()}, Value: v6}, int8, []sqltype.Value{v4}, "22023", ""},
		{sqltype.ArithmeticAdd, parameter, plan.LiteralExpr{Info: expr.Info{Usage: int8.Usage()}, Value: one}, inet, nil, "22003", ""},
		{sqltype.ArithmeticAdd, plan.LiteralExpr{Info: expr.Info{Usage: int8.Usage()}, Value: one}, parameter, inet, nil, "22003", "SQL function \"int8pl_inet\" statement 1"},
	} {
		if tc.frame == nil {
			max, err := inet.Input(ctx, "255.255.255.255", -1)
			if err != nil {
				t.Fatal(err)
			}
			tc.frame = []sqltype.Value{max}
		}
		provider, err := sqltype.BindArithmetic(tc.op, tc.left.ResultUsage().Type, tc.right.ResultUsage().Type, tc.result)
		if err != nil {
			t.Fatal(err)
		}
		n := plan.ArithmeticExpr{Info: expr.Info{Usage: tc.result.Usage(), Property: expr.Properties{ParameterDependent: true}}, Left: tc.left, Right: tc.right, Provider: provider, Operator: tc.op}
		_, _, err = preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: ctx, Parameters: tc.frame}}, n)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Context != tc.context || d.Position != "" {
			t.Fatal(err)
		}
	}
}

func networkOperatorArithmeticStoredValidation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, raw := range []parser.OperatorExpr{
		bitOperatorRaw("+", networkOperatorTyped("inet", "::1"), parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}),
		bitOperatorRaw("+", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, networkOperatorTyped("inet", "::1")),
		bitOperatorRaw("-", networkOperatorTyped("inet", "::1"), networkOperatorTyped("inet", "::")),
	} {
		node, err := b.bindArithmetic(raw)
		if err != nil {
			t.Fatal(err)
		}
		root := node.(plan.ArithmeticExpr)
		for _, mutate := range []func(plan.ArithmeticExpr) plan.ArithmeticExpr{
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Provider = sqltype.Arithmetic{}; return n },
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Operator = sqltype.ArithmeticMultiply; return n },
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Usage.Type = b.typ(sqltype.Int4); return n },
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Left = nil; return n },
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Right = nil; return n },
			func(n plan.ArithmeticExpr) plan.ArithmeticExpr {
				foreign := New()
				left, _ := foreign.CatalogRead().LookupType(n.Left.ResultUsage().Type.ID())
				right, _ := foreign.CatalogRead().LookupType(n.Right.ResultUsage().Type.ID())
				result, _ := foreign.CatalogRead().LookupType(n.Usage.Type.ID())
				n.Provider, err = sqltype.BindArithmetic(n.Operator, left, right, result)
				if err != nil {
					t.Fatal(err)
				}
				return n
			},
		} {
			if _, err := defaultSQL(ctx, e.CatalogRead(), mutate(root)); err == nil {
				t.Fatal("invalid network arithmetic recipe accepted")
			}
		}
	}
}

func networkOperatorOptionalPreflightChildren(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	c := &EvalContext{EvalStatement: evalStatement{Context: ctx}}
	if networkOperatorExecutionConstant(nil, c) {
		t.Fatal("absent root is not an available expression")
	}
	if networkOperatorExecutionConstant(plan.BitwiseExpr{}, c) {
		t.Fatal("absent-only operands are not execution ready")
	}

	node, err := b.bindBitwise(bitOperatorRaw("~", networkOperatorTyped("inet", "192.168.1.6/24")))
	if err != nil {
		t.Fatal(err)
	}
	if !networkOperatorExecutionConstant(node, c) {
		t.Fatal("absent unary right operand blocked readiness")
	}
	folded, changed, err := preflightCoercions(c, node)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	value, err := folded.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Type().Output(ctx, value)
	if err != nil || text != "63.87.254.249/24" {
		t.Fatal(text, err)
	}

	// The same lexical convention applies to optional operands nested below
	// the network root, not only to the network operator's own right child.
	inet, int8 := b.typ(sqltype.Inet), b.typ(sqltype.Int8)
	address, err := inet.Input(ctx, "::2", -1)
	if err != nil {
		t.Fatal(err)
	}
	one, err := sqltype.Integer(int8, 1)
	if err != nil {
		t.Fatal(err)
	}
	negate, err := sqltype.BindArithmetic(sqltype.ArithmeticSubtract, int8, nil, int8)
	if err != nil {
		t.Fatal(err)
	}
	offset := plan.ArithmeticExpr{
		Info:     expr.Info{Usage: int8.Usage()},
		Left:     plan.LiteralExpr{Info: expr.Info{Usage: int8.Usage()}, Value: one},
		Provider: negate, Operator: sqltype.ArithmeticSubtract,
	}
	add, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, inet, int8, inet)
	if err != nil {
		t.Fatal(err)
	}
	root := plan.ArithmeticExpr{
		Info:  expr.Info{Usage: inet.Usage()},
		Left:  plan.LiteralExpr{Info: expr.Info{Usage: inet.Usage()}, Value: address},
		Right: offset, Provider: add, Operator: sqltype.ArithmeticAdd,
	}
	folded, changed, err = preflightCoercions(c, root)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	value, err = folded.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	text, err = inet.Output(ctx, value)
	if err != nil || text != "::1" {
		t.Fatal(text, err)
	}
}
