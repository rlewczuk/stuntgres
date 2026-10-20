package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathHyperbolicBinding(t *testing.T) {
	for _, name := range []string{"sinh", "cosh", "tanh", "asinh", "acosh", "atanh"} {
		for _, input := range []string{"1", "0.5", "'0.5'", "NULL", "$1", "1::smallint", "1::bigint", "1::real"} {
			t.Run(name+"_"+input, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				raw := statement(t, "SELECT "+name+"("+input+")").(*parser.Select).Targets[0]
				node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				u := node.ResultUsage()
				if u.Type.ID() != sqltype.Float8 || u.Typmod != -1 {
					t.Fatal("FLOAT8 result usage", u)
				}
				properties := node.Properties()
				if properties.Volatility != expr.Immutable || properties.RowDependent || properties.StageDependent || properties.ParameterDependent != (input == "$1") {
					t.Fatal("immutable argument properties", properties)
				}
				if input == "$1" && b.Params.Parameters[1] != u {
					t.Fatal("FLOAT8 parameter inference", b.Params.Parameters, u)
				}
			})
		}
	}
}

func TestMathHyperbolicEvaluationPhase(t *testing.T) {
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "input is out of range"}
	for _, query := range []string{"SELECT acosh(0)", "SELECT atanh(2)"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := statement(t, query).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("binding evaluated provider", err)
		}
		c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
		_, err = node.Eval(c)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("execution diagnostic", err)
		}
		_, _, err = preflightCoercions(c, node)
		diagnostic = nil
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("immutable preflight diagnostic", err)
		}
	}
}
