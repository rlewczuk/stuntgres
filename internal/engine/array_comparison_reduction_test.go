package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
	left := literalExpression{Info: b.info(integer.Usage(), parser.Position{}, ExprProperties{}), value: leftValue}
	c := &EvalContext{Context: context.Background()}
	for _, tc := range []struct {
		input string
		null  bool
		want  []Truth
	}{
		{"{}", false, []Truth{False, True, False, True}},
		{"", true, []Truth{Unknown, Unknown, Unknown, Unknown}},
		{"{NULL}", false, []Truth{Unknown, Unknown, Unknown, Unknown}},
		{"{1,NULL}", false, []Truth{True, Unknown, Unknown, False}},
		{"{NULL,1}", false, []Truth{True, Unknown, Unknown, False}},
		{"{2,NULL}", false, []Truth{Unknown, False, True, Unknown}},
		{"{NULL,2}", false, []Truth{Unknown, False, True, Unknown}},
		{"{1,1}", false, []Truth{True, True, False, False}},
		{"{1,2}", false, []Truth{True, False, True, False}},
		{"[-1:0][3:4]={{1,NULL},{2,3}}", false, []Truth{True, False, True, False}},
	} {
		array := sqltype.Null(arrayType)
		if !tc.null {
			array, err = arrayType.Input(c.Context, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		right := literalExpression{Info: b.info(arrayType.Usage(), parser.Position{}, ExprProperties{}), value: array}
		for i, name := range []string{"=", "=", "<>", "<>"} {
			op, _ := comparisonOperator(name)
			predicate, err := sqltype.BindComparisonPredicate(op, integer, integer, boolean, sqltype.CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			node := scalarArrayComparisonExpression{Info: b.info(boolean.Usage(), parser.Position{}, ExprProperties{}), left: left, array: right, predicate: predicate, operator: name, all: i%2 == 1}
			value, err := node.Eval(c)
			if err != nil {
				t.Fatal(tc.input, err)
			}
			truth, err := TruthOf(value)
			if err != nil || truth != tc.want[i] {
				t.Fatal(tc.input, name, node.all, truth, tc.want[i], err)
			}
			node.left = literalExpression{Info: left.Info, value: sqltype.Null(integer)}
			value, err = node.Eval(c)
			if err != nil {
				t.Fatal(err)
			}
			truth, err = TruthOf(value)
			want := Unknown
			if !tc.null && array.ArrayLen() == 0 {
				want = False
				if node.all {
					want = True
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
	node := scalarArrayComparisonExpression{Info: b.info(boolean.Usage(), parser.Position{}, ExprProperties{}), left: left, items: []BoundExpr{left, last}, predicate: predicate, operator: "="}
	value, err := node.Eval(c)
	truth, truthErr := TruthOf(value)
	if err != nil || truthErr != nil || truth != True || calls != 1 {
		t.Fatal("batch must evaluate all input children", truth, calls, err, truthErr)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) {
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
		walkBoundExpression(original, func(child BoundExpr) {
			if p, ok := child.(parameterExpression); ok {
				numbers = append(numbers, p.number)
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
		value, err := mapped.Eval(&EvalContext{Context: context.Background()})
		truth, truthErr := TruthOf(value)
		if err != nil || truthErr != nil || truth != True {
			t.Fatal("predicate not rebound to actual child generations", truth, err, truthErr)
		}
		if !sameGroupExpression(original, original) || sameGroupExpression(original, mapped) {
			t.Fatal("structural identity ignored predicate generations")
		}
		node := original.(scalarArrayComparisonExpression)
		replacement := literalExpression{Info: b.info(node.left.ResultUsage(), parser.Position{}, ExprProperties{}), value: sqltype.Null(node.left.ResultUsage().Type)}
		next, changed, err := mapScalarArrayChildren(node, func(old BoundExpr) (BoundExpr, bool, error) {
			if old == node.left {
				return replacement, true, nil
			}
			return old, false, nil
		})
		if err != nil || !changed || reflect.DeepEqual(next.left, node.left) {
			t.Fatal(next, changed, err)
		}
		value, err = original.Eval(&EvalContext{Context: context.Background()})
		truth, truthErr = TruthOf(value)
		if err != nil || truthErr != nil || truth != True {
			t.Fatal("copy-on-change modified analyzed tree", truth, err, truthErr)
		}
	}
}
