package engine

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func subqueryTestInteger(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func subqueryTestExpression(t *testing.T, e *core.Engine, q *plan.Query, kind parser.SubqueryKind) plan.SubqueryExpr {
	t.Helper()
	typ, ok := e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing BOOL descriptor")
	}
	usage := typ.Usage()
	if kind == parser.SubqueryScalar {
		usage = q.Root.Schema()[0].Usage
	}
	return plan.SubqueryExpr{Info: expr.Info{Usage: usage}, Kind: kind, QueryPlan: q, PresenceOnly: kind == parser.SubqueryExists && analyze.CanSimplifyExists(q)}
}

// Replace an actual scheduler-owned target rather than mocking a query result.
func subqueryTestTarget(t *testing.T, project *plan.ProjectNode, replacement expr.Expr) {
	t.Helper()
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.EvaluationNode:
			if len(n.Expressions) > 0 {
				n.Expressions[0] = replacement
				return
			}
		case *plan.SortNode:
			if len(n.Outputs) > 0 {
				n.Outputs[0] = replacement
				return
			}
		}
		left, right := plan.QueryInputs(node)
		if right != nil {
			t.Fatal("target scheduler crossed a join")
		}
		node = left
	}
	if len(project.Expressions) == 0 {
		t.Fatal("missing target")
	}
	project.Expressions[0] = replacement
}
