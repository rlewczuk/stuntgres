package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIdentifierExtremaInitialization(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []string{"xid", "cid"} {
		for _, function := range []string{"GREATEST", "LEAST"} {
			eng := engine.New()
			b := enginetest.ExpressionBinder(t, ctx, eng, sqltype.InputEnvironment{}, nil)
			parsed, err := parser.Parse("SELECT " + function + "(NULL::" + typ + ",NULL::" + typ + ")")
			if err != nil {
				t.Fatal(err)
			}
			bound, err := b.Bind(parsed[0].(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal("describable extrema rejected at analysis", err)
			}
			node := bound.(plan.ConditionalExpr)
			if !node.MissingOrdering || node.Comparison != (sqltype.Comparison{}) {
				t.Fatal("missing ordering acquired executable comparator", node)
			}
			if _, err := (stored.ScalarRenderer{}).SQL(ctx, eng.CatalogRead(), node); err != nil {
				t.Fatal("unavailable capability lost stored identity", err)
			}
			calls := 0
			node.Info.Property.RowDependent = true
			node.Args = []expr.Expr{conditionalSpy{Info: expr.Info{Usage: node.Usage, Property: expr.Properties{RowDependent: true}}, value: sqltype.Null(node.Usage.Type), calls: &calls, failure: errors.New("argument demanded")}, node.Args[1]}
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
			assert := func(err error) {
				t.Helper()
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || diagnostic.Code != "42883" || diagnostic.Message != "could not identify a comparison function for type "+typ || diagnostic.Position != "" || diagnostic.Hint != "" || diagnostic.Detail != "" || diagnostic.Context != "" || calls != 0 {
					t.Fatal("extrema initialization boundary", diagnostic, err, calls)
				}
			}
			_, _, err = exec.PreflightCoercions(evaluation, node)
			assert(err)
			_, _, err = exec.SimplifyConditional(evaluation, node, false)
			assert(err)
			_, err = node.Eval(evaluation)
			assert(err)
		}
	}
}

// Spies exercise evaluator demand, not a substituted PostgreSQL oracle.
type conditionalSpy struct {
	expr.Info
	value   sqltype.Value
	calls   *int
	failure error
}

func (e conditionalSpy) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := enginetest.EvalContext(ctx)
	if err := exec.EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	*e.calls++
	return e.value, e.failure
}
