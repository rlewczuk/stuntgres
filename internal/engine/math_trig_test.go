package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathTrigBinding(t *testing.T) {
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d"} {
		binary := name == "atan2" || name == "atan2d"
		arity := 1
		if binary {
			arity = 2
		}
		signatures := mathSignatures(name)
		if len(signatures) != 1 || signatures[0].arity != arity || signatures[0].result != sqltype.Float8 || signatures[0].args[0] != sqltype.Float8 || binary && signatures[0].args[1] != sqltype.Float8 {
			t.Fatal("exact FLOAT8 signature", name, signatures)
		}
		for _, args := range []string{"1", "0.5", "'0.5'", "NULL", "$1"} {
			if binary {
				if args == "$1" {
					args += ",$2"
				} else {
					args += ",1"
				}
			}
			query := "SELECT " + name + "(" + args + ")"
			t.Run(query, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				raw := statement(t, query).(*parser.Select).Targets[0]
				node, err := b.bind(raw, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				mathNode, ok := node.(mathExpression)
				if !ok || mathNode.plan.Name() != name || len(mathNode.args) != arity || mathNode.ResultUsage().Type.ID() != sqltype.Float8 || mathNode.ResultUsage().Typmod != -1 {
					t.Fatal("shared math node", node)
				}
				for _, arg := range mathNode.args {
					if arg.ResultUsage() != node.ResultUsage() {
						t.Fatal("coherent FLOAT8 owner", arg.ResultUsage(), node.ResultUsage())
					}
				}
				if args == "$1" || args == "$1,$2" {
					for i := 1; i <= arity; i++ {
						if b.parameters[i] != node.ResultUsage() {
							t.Fatal("held inference", b.parameters)
						}
					}
				}
			})
		}
	}
}

func TestMathTrigEvaluationPhase(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT acos(2)").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal("binding executed a provider", err)
	}
	c := &EvalContext{Context: context.Background()}
	_, err = node.Eval(c)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "input is out of range"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("execution domain diagnostic", err)
	}
	_, _, err = preflightCoercions(c, node)
	diagnostic = nil
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("constant execution preflight", err)
	}
	for _, query := range []string{"SELECT atan2(NULL,asin(2))", "SELECT atan2d(NULL,asind(2))"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("binding demanded constant sibling", err)
		}
		_, _, err = preflightCoercions(c, node)
		diagnostic = nil
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("constant child before strict NULL", query, err)
		}
	}
}

func TestMathTrigRuntimeDemand(t *testing.T) {
	var d *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.Float8 {
			d = candidate
		}
	}
	plan, err := sqltype.BindMath("atan2d", d.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Float(d, 1)
	var order []int
	expr := mathExpression{expressionInfo: expressionInfo{usage: d.Usage()}, plan: plan, args: []BoundExpr{
		mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0},
		mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, value: one, order: &order, index: 1},
	}}
	got, err := expr.Eval(&EvalContext{Context: context.Background()})
	if err != nil || got != sqltype.Null(d) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime NULL demands siblings", got, order, err)
	}
	demanded := errors.New("demanded inverse sibling")
	expr.args[1] = mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, order: &order, index: 1, err: demanded}
	order = nil
	_, err = expr.Eval(&EvalContext{Context: context.Background()})
	if !errors.Is(err, demanded) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime demand failure", order, err)
	}
	expr.args[0] = literalExpression{expressionInfo{usage: d.Usage()}, sqltype.Null(d)}
	order = nil
	got, err = expr.Eval(&EvalContext{Context: context.Background()})
	if err != nil || got != sqltype.Null(d) || len(order) != 0 {
		t.Fatal("literal NULL eliminated runtime sibling", got, order, err)
	}
}
