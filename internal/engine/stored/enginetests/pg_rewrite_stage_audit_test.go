package enginetests_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestPGRewriteQueryStageAssociations(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		fragments []string
	}{
		{"sort", "SELECT v FROM audit_input ORDER BY v LIMIT 1", []string{"key 0 output", "target", "class"}},
		{"distinct", "SELECT DISTINCT v FROM audit_input", []string{"grouping 0", "collation"}},
		{"set", "SELECT v FROM audit_input UNION SELECT v+1 FROM audit_input", []string{"plan.SetNode", "grouping 0"}},
		{"correlation", "SELECT a.v,q.physical FROM audit_input a CROSS JOIN LATERAL unnest(ARRAY[a.v]) q(physical)", []string{"capture frame", "correlation_prefix"}},
		{"set_projection", "SELECT generate_series(1,v) AS n FROM audit_input", []string{"plan.SetProjectNode", "call_slots", "group"}},
		{"recursive", "WITH RECURSIVE q(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM q WHERE n<3) SELECT n FROM q", []string{"plan.RecursiveUnionNode", "backedge", "cte owner"}},
		{"search_cycle", "WITH RECURSIVE q(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM q WHERE n<3) SEARCH DEPTH FIRST BY n SET ord CYCLE n SET cyc USING path SELECT n FROM q", []string{"search_slot", "cycle_slot", "equality 0", "cycle_inequality_collation"}},
		{"locking", "SELECT v FROM audit_input FOR UPDATE NOWAIT", []string{"lock 0 mode", "wait"}},
		{"window_frame", "SELECT v,sum(v) OVER(ORDER BY v RANGE BETWEEN 1 PRECEDING AND CURRENT ROW) AS s FROM audit_input", []string{"window call 0 frame 1 slot 1 occurrence 0", "specs [0] sorts [0]", "bound 0 kind 1 key 23 offset 23 class 1978", "key 0 output 0 target 0 slot 0 cache 0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE TABLE audit_input(v integer)")
			enginetest.SessionSQL(t, session, "CREATE VIEW audit_view AS "+tc.sql)
			read := e.CatalogRead()
			owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "audit_view").Address)
			definition := stored.ViewOf(owner)
			var out strings.Builder
			if err := plan.AppendQueryTree(t.Context(), &out, definition.Query().Root); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(out.String(), fragment) {
					t.Fatal("missing actual stage association", fragment, out.String())
				}
			}
			var visit func(plan.Node)
			visit = func(node plan.Node) {
				if node == nil {
					return
				}
				var original, changed plan.Node
				switch n := node.(type) {
				case *plan.SortNode:
					if len(n.Keys) > 0 {
						copy := *n
						copy.Keys = append([]plan.SortKey(nil), n.Keys...)
						copy.Keys[0].TargetIdentity++
						original, changed = n, &copy
					}
				case *plan.EvaluationNode:
					copy := *n
					copy.Base++
					original, changed = n, &copy
				case *plan.DerivedNode:
					copy := *n
					copy.ScanFrame++
					original, changed = n, &copy
				case *plan.JoinNode:
					copy := *n
					copy.CorrelationPrefix++
					original, changed = n, &copy
				case *plan.SetProjectNode:
					copy := *n
					copy.Frame++
					original, changed = n, &copy
				}
				if original != nil {
					var a, b strings.Builder
					if err := plan.AppendQueryTree(t.Context(), &a, original); err != nil {
						t.Fatal(err)
					}
					if err := plan.AppendQueryTree(t.Context(), &b, changed); err != nil {
						t.Fatal(err)
					}
					if a.String() == b.String() {
						t.Fatal("lost analyzed association", fmt.Sprintf("%T", node))
					}
				}
				left, right := plan.QueryInputs(node)
				visit(left)
				visit(right)
			}
			visit(definition.Query().Root)
			// The expression and action routes use exactly the same query writer.
			boolean, _ := read.LookupType(16)
			sublink := plan.SubqueryExpr{Info: expr.Info{Usage: boolean.Usage()}, QueryPlan: definition.Query()}
			text, err := (plan.BoundTree{Expression: sublink}).StructuralOutput(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, out.String()) {
				t.Fatal("subquery used a second or narrowed query serializer", text, out.String())
			}
		})
	}
}
