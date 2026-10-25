package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMACBitwiseReownership(t *testing.T) {
	ctx := context.Background()
	e, replacement := engine.New(), engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	mapper := stored.ViewQueryRemapper{Catalog: replacement.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(e, replacement)}
	for _, width := range []struct {
		name, input string
		id          sqltype.ID
	}{
		{"macaddr", "12:34:56:78:90:ab", sqltype.Macaddr},
		{"macaddr8", "92:34:56:78:90:ab:cd:ef", sqltype.Macaddr8},
	} {
		for _, op := range []string{"~", "&", "|"} {
			args := []parser.Expr{networkOperatorTyped(width.name, width.input)}
			if op != "~" {
				args = append(args, networkOperatorTyped(width.name, width.input))
			}
			node, err := b.Bind(bitOperatorRaw(op, args...), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			root := node.(plan.BitwiseExpr)
			if root.MAC == (sqltype.MACBitwise{}) || root.Provider != (sqltype.Bitwise{}) || root.Bits != (sqltype.BitStringBitwise{}) || root.Network != (sqltype.NetworkBitwise{}) {
				t.Fatal("provider ownership")
			}
			if root.CallRoutineReference() == nil || root.ResultUsage() != b.Type(width.id).Usage() {
				t.Fatal("routine/type identity")
			}
			if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), root); err != nil {
				t.Fatal(err)
			}
			rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), root, func(d *sqltype.Descriptor) *sqltype.Descriptor {
				next, _ := replacement.CatalogRead().LookupType(d.ID())
				return next
			})
			if err != nil || !changed || rebound.ResultUsage().Type == root.ResultUsage().Type {
				t.Fatal(changed, err)
			}
			query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{rebound}}})
			if err != nil {
				t.Fatal(err)
			}
			rebound = query.(*plan.ValuesNode).Rows[0][0]
			next := rebound.(plan.BitwiseExpr)
			if next.MAC == root.MAC || next.CallRoutineReference() == nil || next.CallRoutineReference().RoutineOID() != root.CallRoutineReference().RoutineOID() {
				t.Fatal("retag authority")
			}
			before, err := root.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			after, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			want, err := before.Retag(next.ResultUsage().Type)
			if err != nil || after != want {
				t.Fatal(after, want, err)
			}
			if _, err := stored.DefaultSQL(ctx, replacement.CatalogRead(), next); err != nil {
				t.Fatal(err)
			}
			if !b.SameGroupExpression(root, root) {
				t.Fatal("self equality")
			}
			bad := root
			bad.MAC = next.MAC
			if b.SameGroupExpression(root, bad) {
				t.Fatal("MAC provider omitted from structural equality")
			}
			integral, err := sqltype.BindBitwise(sqltype.BitwiseAnd, b.Type(sqltype.Int4), b.Type(sqltype.Int4))
			if err != nil {
				t.Fatal(err)
			}
			bits, err := sqltype.BindBitStringBitwise(sqltype.BitwiseAnd, b.Type(sqltype.Bit), b.Type(sqltype.Bit))
			if err != nil {
				t.Fatal(err)
			}
			network, err := sqltype.BindNetworkBitwise(sqltype.BitwiseAnd, b.Type(sqltype.Inet), b.Type(sqltype.Inet))
			if err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(plan.BitwiseExpr) plan.BitwiseExpr{
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.MAC = sqltype.MACBitwise{}; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.MAC = next.MAC; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Provider = integral; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Bits = bits; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Network = network; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Left = nil; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseXor; return n },
				func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Usage.Typmod = 4; return n },
			} {
				if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), mutate(root)); err == nil {
					t.Fatal("corrupt MAC recipe admitted")
				}
			}
			for _, raw := range []parser.OperatorExpr{
				bitOperatorRaw("&", bitOperatorLiteral("1"), bitOperatorLiteral("1")),
				bitOperatorRaw("&", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}),
				bitOperatorRaw("&", networkOperatorTyped("inet", "::1"), networkOperatorTyped("inet", "::1")),
			} {
				other, err := b.BindBitwise(raw)
				if err != nil {
					t.Fatal(err)
				}
				corrupt := other.(plan.BitwiseExpr)
				corrupt.MAC = root.MAC
				if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), corrupt); err == nil {
					t.Fatal("inactive MAC provider admitted")
				}
			}
		}
	}
	// Actual bound function reconstruction preserves source829/result774 and
	// uses the routine renderer's authority check, not a private duplicate.
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "macaddr8_set7bit"}}}, Args: []parser.Expr{networkOperatorTyped("macaddr", "00:34:56:ab:cd:ef")}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := node.(functions.MACUnary)
	rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), root, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := replacement.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{rebound}}})
	if err != nil {
		t.Fatal(err)
	}
	rebound = query.(*plan.ValuesNode).Rows[0][0]
	next := rebound.(functions.MACUnary)
	if next.Arg.ResultUsage().Type.ID() != sqltype.Macaddr || next.ResultUsage().Type.ID() != sqltype.Macaddr8 || next.Provider == root.Provider {
		t.Fatal("function-owned expansion retag")
	}
	if _, err := stored.DefaultSQL(ctx, replacement.CatalogRead(), next); err != nil {
		t.Fatal(err)
	}
	bad := root
	bad.RoutineReference = nil
	if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), bad); err == nil {
		t.Fatal("missing routine authority admitted")
	}
	// A strict row NULL never suppresses the second child's error or order.
	d := b.Type(sqltype.Macaddr)
	provider, err := sqltype.BindMACBitwise(sqltype.BitwiseAnd, d, d)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	left := queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { calls = append(calls, "left"); return sqltype.Null(d), nil })
	failure := errors.New("MAC right child")
	right := queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) {
		calls = append(calls, "right")
		return sqltype.Value{}, failure
	})
	binary := plan.BitwiseExpr{Info: expr.Info{Usage: d.Usage()}, Left: left, Right: right, MAC: provider, Operator: sqltype.BitwiseAnd}
	if _, err := binary.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, failure) || !reflect.DeepEqual(calls, []string{"left", "right"}) {
		t.Fatal(calls, err)
	}
}
