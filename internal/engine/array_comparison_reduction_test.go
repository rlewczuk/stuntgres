package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayCompareReduction(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer, boolean := b.typ(sqltype.Int4), b.typ(sqltype.Bool)
	arrayType := b.typ(integer.AssociatedArray())
	leftValue, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	left := plan.LiteralExpr{Info: b.info(integer.Usage(), parser.Position{}, expr.Properties{}), Value: leftValue}
	c := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}}
	for _, tc := range []struct {
		input string
		null  bool
		want  []plan.Truth
	}{
		{"{}", false, []plan.Truth{plan.False, plan.True, plan.False, plan.True}},
		{"", true, []plan.Truth{plan.Unknown, plan.Unknown, plan.Unknown, plan.Unknown}},
		{"{NULL}", false, []plan.Truth{plan.Unknown, plan.Unknown, plan.Unknown, plan.Unknown}},
		{"{1,NULL}", false, []plan.Truth{plan.True, plan.Unknown, plan.Unknown, plan.False}},
		{"{NULL,1}", false, []plan.Truth{plan.True, plan.Unknown, plan.Unknown, plan.False}},
		{"{2,NULL}", false, []plan.Truth{plan.Unknown, plan.False, plan.True, plan.Unknown}},
		{"{NULL,2}", false, []plan.Truth{plan.Unknown, plan.False, plan.True, plan.Unknown}},
		{"{1,1}", false, []plan.Truth{plan.True, plan.True, plan.False, plan.False}},
		{"{1,2}", false, []plan.Truth{plan.True, plan.False, plan.True, plan.False}},
		{"[-1:0][3:4]={{1,NULL},{2,3}}", false, []plan.Truth{plan.True, plan.False, plan.True, plan.False}},
	} {
		array := sqltype.Null(arrayType)
		if !tc.null {
			array, err = arrayType.Input(c.Context, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		right := plan.LiteralExpr{Info: b.info(arrayType.Usage(), parser.Position{}, expr.Properties{}), Value: array}
		for i, name := range []string{"=", "=", "<>", "<>"} {
			op, _ := comparisonOperator(name)
			predicate, err := sqltype.BindComparisonPredicate(op, integer, integer, boolean, sqltype.CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.ScalarArrayComparisonExpr{Info: b.info(boolean.Usage(), parser.Position{}, expr.Properties{}), Left: left, Array: right, Predicate: predicate, Operator: name, All: i%2 == 1}
			value, err := node.Eval(c)
			if err != nil {
				t.Fatal(tc.input, err)
			}
			truth, err := plan.TruthOf(value)
			if err != nil || truth != tc.want[i] {
				t.Fatal(tc.input, name, node.All, truth, tc.want[i], err)
			}
			node.Left = plan.LiteralExpr{Info: left.Info, Value: sqltype.Null(integer)}
			value, err = node.Eval(c)
			if err != nil {
				t.Fatal(err)
			}
			truth, err = plan.TruthOf(value)
			want := plan.Unknown
			if !tc.null && array.ArrayLen() == 0 {
				want = plan.False
				if node.All {
					want = plan.True
				}
			}
			if err != nil || truth != want {
				t.Fatal("NULL left", tc.input, truth, want, err)
			}
		}
		if !tc.null {
			text, err := arrayType.Output(c.Context, array)
			if err != nil || text != tc.input {
				t.Fatal("array payload mutated", text, tc.input, err)
			}
		}
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	last := queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; return leftValue, nil })
	node := plan.ScalarArrayComparisonExpr{Info: b.info(boolean.Usage(), parser.Position{}, expr.Properties{}), Left: left, Items: []expr.Expr{left, last}, Predicate: predicate, Operator: "="}
	value, err := node.Eval(c)
	truth, truthErr := plan.TruthOf(value)
	if err != nil || truthErr != nil || truth != plan.True || calls != 1 {
		t.Fatal("batch must evaluate all input children", truth, calls, err, truthErr)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: canceled}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestArrayCompareBoundVisitors(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, q := range []string{"SELECT $1 IN(1,$2,3)", "SELECT $1=ANY($2)"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, q).(*parser.Select).Targets[0]
		original, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		var numbers []int
		plan.WalkExpression(original, func(child expr.Expr) {
			if p, ok := child.(plan.ParameterExpr); ok {
				numbers = append(numbers, p.Number)
			}
		})
		if !reflect.DeepEqual(numbers, []int{1, 2}) {
			t.Fatal("lost RHS traversal", numbers)
		}
	}
	for _, q := range []string{"SELECT 1 IN(1,2,NULL)", "SELECT 1=ANY('{1,NULL}'::INT[])"} {
		original, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		other := New()
		read := other.CatalogRead()
		mapped, changed, err := retagBoundTypes(read, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			next, _ := read.LookupType(d.ID())
			return next
		})
		if err != nil || !changed || mapped.ResultUsage().Type == original.ResultUsage().Type {
			t.Fatal(mapped, changed, err)
		}
		value, err := mapped.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
		truth, truthErr := plan.TruthOf(value)
		if err != nil || truthErr != nil || truth != plan.True {
			t.Fatal("predicate not rebound to actual child generations", truth, err, truthErr)
		}
		if !sameGroupExpression(original, original) || sameGroupExpression(original, mapped) {
			t.Fatal("structural identity ignored predicate generations")
		}
		node := original.(plan.ScalarArrayComparisonExpr)
		replacement := plan.LiteralExpr{Info: b.info(node.Left.ResultUsage(), parser.Position{}, expr.Properties{}), Value: sqltype.Null(node.Left.ResultUsage().Type)}
		next, changed, err := mapScalarArrayChildren(node, func(old expr.Expr) (expr.Expr, bool, error) {
			if old == node.Left {
				return replacement, true, nil
			}
			return old, false, nil
		})
		if err != nil || !changed || reflect.DeepEqual(next.Left, node.Left) {
			t.Fatal(next, changed, err)
		}
		value, err = original.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
		truth, truthErr = plan.TruthOf(value)
		if err != nil || truthErr != nil || truth != plan.True {
			t.Fatal("copy-on-change modified analyzed tree", truth, err, truthErr)
		}
	}
}
