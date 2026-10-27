package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func explainAliasOwnership(t *testing.T) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE explain_alias_source(id integer)")
	for _, tc := range []struct{ name, query string }{
		{"derived", "SELECT x.k FROM (SELECT id FROM explain_alias_source) x(k)"},
		{"cte", "WITH x AS MATERIALIZED (SELECT id FROM explain_alias_source) SELECT a.k,b.k FROM x a(k) JOIN x b(k) ON a.k=b.k"},
		{"values", "SELECT a.k,b.k FROM (VALUES(1)) a(k) CROSS JOIN (VALUES(2)) b(k)"},
		{"function", "SELECT g FROM generate_series(1,2) g"},
	} {
		t.Run("bound-alias-"+tc.name, func(t *testing.T) {
			ast := enginetest.Statement(t, tc.query)
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if binding.Query == nil {
				t.Fatal("missing actual bound query")
			}
			var derived []*plan.DerivedNode
			var values []*plan.ProjectNode
			var functions []*plan.FunctionFromNode
			plan.VisitExecutableQuery(binding.Query.Root, func(node plan.Node) {
				switch node := node.(type) {
				case *plan.DerivedNode:
					if node.RangeName != "" {
						derived = append(derived, node)
					}
				case *plan.ProjectNode:
					if node.RangeName != "" {
						values = append(values, node)
					}
				case *plan.FunctionFromNode:
					functions = append(functions, node)
				}
			}, func(expr.Expr) {})
			switch tc.name {
			case "derived":
				if len(derived) != 1 || derived[0].RangeName != "x" || len(derived[0].ColumnAliases) != 1 || derived[0].ColumnAliases[0] != "k" || derived[0].Schema()[0].Name != "id" {
					t.Fatal("derived alias mutated schema or lost occurrence", derived)
				}
			case "cte":
				if len(derived) != 2 || derived[0] == derived[1] || derived[0].CTE == nil || derived[0].CTE != derived[1].CTE || derived[0].RangeName != "a" || derived[1].RangeName != "b" || derived[0].ColumnAliases[0] != "k" || derived[1].ColumnAliases[0] != "k" {
					t.Fatal("shared producer erased distinct alias occurrences", derived)
				}
			case "values":
				if len(values) != 2 || values[0] == values[1] || values[0].RangeName != "a" || values[1].RangeName != "b" || values[0].ColumnAliases[0] != "k" || values[1].ColumnAliases[0] != "k" || values[0].Schema()[0].Name != "column1" || values[1].Schema()[0].Name != "column1" {
					t.Fatal("VALUES alias changed actual stage/schema", values)
				}
			case "function":
				if len(functions) != 1 || functions[0].RangeName != "g" || len(functions[0].ColumnAliases) != 1 || functions[0].ColumnAliases[0] != "g" || len(functions[0].Calls) != 1 || functions[0].Calls[0].ResultUsage().Type == nil {
					t.Fatal("function alias lost accepted callable occurrence", functions)
				}
			}
		})
	}
}
