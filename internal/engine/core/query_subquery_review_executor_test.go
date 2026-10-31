package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQuerySubquerySecondProjectedTuple(t *testing.T) {
	for _, kind := range []parser.SubqueryKind{parser.SubqueryScalar, parser.SubqueryRow} {
		name := "scalar"
		if kind == parser.SubqueryRow {
			name = "row"
		}
		for _, failure := range []string{"valid", "projection", "validation", "cancel"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				e, q, source := queryJoinsFixture(t, "SELECT k FROM a")
				project := q.Root.(*plan.ProjectNode)
				usage := project.Schema()[0].Usage
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				primary := errors.New("second projection")
				calls := 0
				project.Expressions[0] = enginetest.QueryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 2 {
						switch failure {
						case "projection":
							return sqltype.Value{}, primary
						case "validation":
							return sqltype.Value{}, nil
						case "cancel":
							cancel()
							return sqltype.Value{}, context.Canceled
						}
					}
					return sqltype.Integer(usage.Type, 1)
				})
				bound := subqueryTestExpression(t, e, q, kind)
				if kind == parser.SubqueryRow {
					predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, usage.Type, usage.Type, bound.Usage.Type, usage.Collation, false)
					if err != nil {
						t.Fatal(err)
					}
					bound.Comparisons = []plan.ComparisonExpr{{Left: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: subqueryTestInteger(t, usage.Type, 1)}, Right: plan.ColumnExpr{Info: expr.Info{Usage: usage}, Scope: 1, Frame: project.Frame, Slot: 0}, Predicate: predicate}}
				}
				parent := exec.State{Source: source}
				c := parent.Evaluation(ctx)
				_, err := bound.Eval(&c)
				if calls != 2 {
					t.Fatal("must project exactly two tuples", calls, err)
				}
				switch failure {
				case "projection":
					if err != primary {
						t.Fatal("projection error lost", err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation lost", err)
					}
				case "validation":
					var diagnostic *sqlstate.Error
					if err == nil || errors.As(err, &diagnostic) && diagnostic.Code == "21000" {
						t.Fatal("second validation skipped", err)
					}
				case "valid":
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "21000" {
						t.Fatal("missing cardinality error", err)
					}
				}
			})
		}
	}
}

func TestQuerySubqueryLiteralDemandBoundary(t *testing.T) {
	e, query, source := queryJoinsFixture(t, "SELECT TRUE FROM a")
	child := subqueryTestExpression(t, e, query, parser.SubqueryScalar)
	typ := child.Usage.Type
	info := expr.Info{Usage: typ.Usage()}
	falseLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(typ, false)}
	trueLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(typ, true)}
	nullLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Null(typ)}
	parameter := plan.ParameterExpr{Info: info, Number: 1}
	for _, tc := range []struct {
		name        string
		op          parser.LogicalOperator
		left, right expr.Expr
		want        bool
		state       string
	}{
		{"false-left", parser.LogicalAnd, falseLiteral, child, false, ""},
		{"true-left", parser.LogicalOr, trueLiteral, child, true, ""},
		{"null-left", parser.LogicalAnd, nullLiteral, child, false, "21000"},
		{"parameter-left", parser.LogicalAnd, parameter, child, false, "21000"},
		{"false-right", parser.LogicalAnd, child, falseLiteral, false, "21000"},
		{"not", parser.LogicalNot, child, nil, false, "21000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := exec.State{Source: source}
			c := parent.Evaluation(context.Background())
			c.Parameters = []sqltype.Value{sqltype.Boolean(typ, false)}
			expression := plan.LogicalExpr{Info: info, Op: tc.op, Left: tc.left, Right: tc.right, QueryRight: tc.right != nil}
			value, err := expression.Eval(&c)
			if tc.state != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.state {
					t.Fatal("nondecisive operand lost child cardinality", err)
				}
				return
			}
			actual, valueErr := value.Boolean()
			if err != nil || valueErr != nil || value.Type() != typ || actual != tc.want {
				t.Fatal(value, err, valueErr)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := expression.Eval(&c); !errors.Is(err, context.Canceled) {
				t.Fatal("decisive literal bypassed cancellation", err)
			}
			c.Context = context.Background()
			expression.Right = nil
			if _, err := expression.Eval(&c); err == nil {
				t.Fatal("decisive literal bypassed logical shape validation")
			}
		})
	}
}
