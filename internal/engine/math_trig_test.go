package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathTrigBinding(t *testing.T) {
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand"} {
		binary := name == "atan2" || name == "atan2d"
		arity := 1
		if binary {
			arity = 2
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
				node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				mathNode, ok := node.(functions.Math)
				if !ok || mathNode.Plan.Name() != name || len(mathNode.Args) != arity || mathNode.ResultUsage().Type.ID() != sqltype.Float8 || mathNode.ResultUsage().Typmod != -1 {
					t.Fatal("shared math node", node)
				}
				for _, arg := range mathNode.Args {
					if arg.ResultUsage() != node.ResultUsage() {
						t.Fatal("coherent FLOAT8 owner", arg.ResultUsage(), node.ResultUsage())
					}
				}
				if args == "$1" || args == "$1,$2" {
					for i := 1; i <= arity; i++ {
						if b.Params.Parameters[i] != node.ResultUsage() {
							t.Fatal("held inference", b.Params.Parameters)
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
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal("binding executed a provider", err)
	}
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	_, err = node.Eval(c)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "input is out of range"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("execution domain diagnostic", err)
	}
	_, _, err = exec.PreflightCoercions(c, node)
	diagnostic = nil
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("constant execution preflight", err)
	}
	for _, query := range []string{"SELECT atan2(NULL,asin(2))", "SELECT atan2d(NULL,asind(2))", "SELECT atan2(NULL,sin('Infinity'))", "SELECT atan2d(NULL,sind('Infinity'))"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.Bind(statement(t, query).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("binding demanded constant sibling", err)
		}
		_, _, err = exec.PreflightCoercions(c, node)
		diagnostic = nil
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("constant child before strict NULL", query, err)
		}
	}
	for _, name := range []string{"cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand"} {
		t.Run(name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			raw := statement(t, "SELECT "+name+"('Infinity')").(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal("binding executed direct provider", err)
			}
			_, err = node.Eval(c)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("direct execution domain diagnostic", err)
			}
			_, _, err = exec.PreflightCoercions(c, node)
			diagnostic = nil
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("direct constant execution preflight", err)
			}
		})
	}
}
