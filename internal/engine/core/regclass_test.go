package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRegclassConstructorShapeOwnershipAndWalkers(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.ArrayExpr{Elements: []parser.Expr{parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
	node, err := b.BindArray(raw, sqltype.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	if value.IsNull() || value.ArrayLen() != 2 || !value.ArrayElement(1).IsNull() || !reflect.DeepEqual(value.ArrayDimensions(), []sqltype.ArrayDimension{{Length: 2, Lower: 1}}) {
		t.Fatal(value, err)
	}
	dimensions := value.ArrayDimensions()
	dimensions[0].Lower = 99
	if value.ArrayDimensions()[0].Lower != 1 {
		t.Fatal("dimension accessor mutates owned datum")
	}
	count := 0
	plan.ExpressionChildren(node, func(expr.Expr) { count++ })
	if count != 2 {
		t.Fatal(count)
	}
	if exec.PlannedVolatility(node) != expr.Immutable || !plan.GenerationImmutable(node) || !table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: node}) || !b.SameGroupExpression(node, node) {
		t.Fatal("constructor properties/walkers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	empty, err := b.BindArray(parser.ArrayExpr{}, node.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	emptyValue, err := empty.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || emptyValue.IsNull() || emptyValue.ArrayLen() != 0 || len(emptyValue.ArrayDimensions()) != 0 {
		t.Fatal(emptyValue, err)
	}
}

func TestRegclassConstructorParameterOccurrences(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		for _, repeated := range []bool{false, true} {
			e := engine.New()
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			typ := b.Type(sqltype.Regclass)
			raw := parser.ArrayExpr{Elements: []parser.Expr{parser.ParameterExpr{Number: 1}}}
			expected := sqltype.Usage{}
			if contextual {
				expected = b.Type(typ.AssociatedArray()).Usage()
			} else {
				raw.Elements = append(raw.Elements, parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}})
			}
			if repeated {
				raw.Elements = append(raw.Elements, parser.ParameterExpr{Number: 1})
			}
			node, err := b.BindArray(raw, expected)
			if err != nil {
				t.Fatal(contextual, repeated, err)
			}
			parameters, err := b.Finish()
			if err != nil || parameters.Count != 1 || parameters.Types[0].Type != typ || !node.Properties().ParameterDependent {
				t.Fatal(parameters, node.Properties(), err)
			}
			for _, parameter := range []sqltype.Value{sqltype.String(typ, "0"), sqltype.Null(typ), sqltype.String(typ, "1259")} {
				value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{parameter}}})
				if err != nil || value.ArrayLen() != len(raw.Elements) {
					t.Fatal(value, err)
				}
				first := value.ArrayElement(0)
				if first.Type() != typ || first.IsNull() != parameter.IsNull() {
					t.Fatal(first, parameter)
				}
				if repeated {
					last := value.ArrayElement(value.ArrayLen() - 1)
					if !reflect.DeepEqual(first, last) {
						t.Fatal("shared slot occurrences changed", first, last)
					}
				}
			}
		}
	}
}
