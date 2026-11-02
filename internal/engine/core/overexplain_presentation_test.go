package core_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func explainDebugOwnership(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, sql string
		targets   []string
	}{
		{"ordered", "SELECT a.id,a.v FROM overexplain_owner a WHERE a.id>0 ORDER BY a.id", []string{"a.id", "a.v"}},
		{"self-join", "SELECT a.id,b.v FROM overexplain_owner a JOIN overexplain_owner b ON a.id=b.id ORDER BY a.id", []string{"a.id", "b.v"}},
		{"derived", "SELECT d.id,d.v FROM (SELECT id,v FROM overexplain_owner) d", []string{"d.id", "d.v"}},
		{"cte", "WITH c AS MATERIALIZED (SELECT id,v FROM overexplain_owner) SELECT a.id,b.v FROM c a JOIN c b ON a.id=b.id", []string{"a.id", "b.v"}},
		{"subplan", "SELECT a.id,(SELECT b.v FROM overexplain_owner b WHERE b.id=a.id) FROM overexplain_owner a", nil},
		{"zero-width", "SELECT", []string{}},
		{"function-frame", "SELECT g FROM generate_series(1,2) g", nil},
		{"set-frame", "SELECT generate_series(1,2) AS g", nil},
	} {
		t.Run("debug-bound-"+tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE overexplain_owner(id integer,v text)")
			statement := enginetest.Statement(t, tc.sql)
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					scope.Finish(context.Canceled)
				}
			}()
			description, err := scope.Describe(statement)
			if err != nil {
				t.Fatal(err)
			}
			defer e.RetirePreparedDescription(&description)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement, auth, ids.ObjectRef{}, scope.Input, nil, nil)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			root := binding.Query.Root
			actual := make(map[plan.Node]bool)
			sourceRefs := make(map[*plan.SourceNode]ids.ObjectRef)
			var collect func(plan.Node)
			collect = func(root plan.Node) {
				if root == nil || actual[root] {
					return
				}
				plan.VisitExecutableQuery(root, func(node plan.Node) {
					actual[node] = true
					if source, ok := node.(*plan.SourceNode); ok {
						sourceRefs[source] = source.Ref
					}
					if derived, ok := node.(*plan.DerivedNode); ok && derived.CTE != nil && derived.CTE.Query != nil {
						collect(derived.CTE.Query.Root)
					}
				}, func(expr.Expr) {})
			}
			collect(root)
			project := root.(*plan.ProjectNode)
			frame, child := project.Frame, project.Child
			expressions := slices.Clone(project.Expressions)
			var expressionBacking *expr.Expr
			if len(project.Expressions) != 0 {
				expressionBacking = &project.Expressions[0]
			}
			var sortOwner *plan.SortNode
			var keys []plan.SortKey
			var keyBacking *plan.SortKey
			if n, ok := project.Child.(*plan.SortNode); ok {
				sortOwner, keys = n, slices.Clone(n.Keys)
				if len(n.Keys) != 0 {
					keyBacking = &n.Keys[0]
				}
			}
			options := explain.Options{Debug: true, Format: "json"}
			host, ok := core.HostOf(scope).(explain.Host)
			if !ok {
				e.Mu.Unlock()
				t.Fatal("actual statement host lacks EXPLAIN authority")
			}
			renderer, err := explain.NewRenderer(host, auth, options)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			_, err = renderer.Command(statement, explain.Bound{Binding: binding})
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			// Actual pointers come from the borrowed presentation, not a second
			// parser/binder or reconstructed expected graph.
			nodes := renderer.PresentedNodes()
			if len(nodes) == 0 || nodes[0] != root {
				e.Mu.Unlock()
				t.Fatal("renderer replaced actual bound root")
			}
			typ, _ := auth.Catalog.LookupType(sqltype.JSON)
			schema := []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}
			e.Mu.Unlock()
			rows, err := explain.Output(op.Ctx, schema, options, []*explain.Renderer{renderer}, nil, explain.Summaries{})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0].IsNull() {
				t.Fatal("missing owned debug datum")
			}
			retained := rows[0][0]
			text, err := retained.Type().Output(op.Ctx, retained)
			if err != nil {
				t.Fatal(err)
			}
			var document []map[string]any
			if err := json.Unmarshal([]byte(text), &document); err != nil {
				t.Fatal(err)
			}
			var projected []map[string]any
			var visit func(map[string]any)
			visit = func(n map[string]any) {
				if d, ok := n["Embedded Debug"].(map[string]any); ok {
					if n["Node Type"] == "SubPlan" || n["Node Type"] == "Rule Rewrite" {
						t.Fatal("synthetic wrapper got graph identity")
					}
					projected = append(projected, d)
				}
				children, _ := n["Plans"].([]any)
				for _, c := range children {
					visit(c.(map[string]any))
				}
			}
			for _, entry := range document {
				visit(entry["Plan"].(map[string]any))
			}
			if len(projected) != len(nodes) {
				t.Fatalf("actual/projected node cardinality %d/%d", len(nodes), len(projected))
			}
			identities := map[plan.Node]int{}
			for i, node := range nodes {
				if !actual[node] {
					t.Fatal("renderer substituted a fabricated node for the actual borrowed graph")
				}
				d := projected[i]
				id := identities[node]
				if id == 0 {
					id = len(identities) + 1
					identities[node] = id
				}
				if d["Node ID"] != float64(id) {
					t.Fatal("identity not derived from actual first presentation encounter", d, id)
				}
				switch n := node.(type) {
				case *plan.ProjectNode:
					if d["Frame"] != float64(n.Frame) {
						t.Fatal("project frame invented", d, n.Frame)
					}
					list, ok := d["Target List"].([]any)
					if !ok || len(list) != len(n.Expressions) {
						t.Fatal("actual project targets/empty list lost", d)
					}
				case *plan.SortNode:
					list, ok := d["Target List"].([]any)
					if !ok || len(list) != len(n.Outputs) {
						t.Fatal("actual sort output ownership lost", d)
					}
					ordered := d["Sort Targets"].([]any)
					if len(ordered) != len(n.Keys) {
						t.Fatal("actual sort key count lost")
					}
					for j, key := range n.Keys {
						m := ordered[j].(map[string]any)
						for label, want := range map[string]int{"Target Identity": key.TargetIdentity, "Output Ordinal": key.OutputOrdinal, "Slot": key.Slot, "Cache": key.Cache} {
							if m[label] != float64(want) {
								t.Fatal("sort key field not from actual owner", label, m, want)
							}
						}
						if m["Output"] != key.Output || !strings.Contains(m["Expression"].(string), "a.id") {
							t.Fatal("sort key expression/output ownership lost", m)
						}
					}
				case *plan.SourceNode:
					if d["Occurrence"] != float64(n.Occurrence) || d["Relation OID"] != float64(n.Ref.Address.ObjectID) {
						t.Fatal("source identity/zero occurrence lost", d, n.Ref)
					}
					if _, exists := d["Frame"]; exists {
						t.Fatal("source fabricated a frame", d)
					}
				case *plan.DerivedNode:
					if d["Frame"] != float64(n.ScanFrame) {
						t.Fatal("derived frame invented", d)
					}
				case *plan.JoinNode:
					if d["Frame"] != float64(n.Frame) {
						t.Fatal("join frame invented", d)
					}
				case *plan.FunctionFromNode:
					if d["Frame"] != float64(n.Frame) {
						t.Fatal("function frame invented", d)
					}
				case *plan.SetProjectNode:
					if d["Frame"] != float64(n.Frame) {
						t.Fatal("set projection frame invented", d)
					}
				}
			}
			if tc.targets != nil {
				got := projected[0]["Target List"].([]any)
				want := make([]any, len(tc.targets))
				for i, target := range tc.targets {
					want[i] = target
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("target SQL did not retain actual lexical aliases/order", got, want)
				}
			}
			if binding.Query.Root != root || project.Frame != frame || project.Child != child || !reflect.DeepEqual(project.Expressions, expressions) || expressionBacking != nil && &project.Expressions[0] != expressionBacking {
				t.Fatal("debug mutated graph/frame/expression backing")
			}
			if sortOwner != nil && (!reflect.DeepEqual(sortOwner.Keys, keys) || keyBacking != nil && &sortOwner.Keys[0] != keyBacking) {
				t.Fatal("debug mutated actual sort/cache owners")
			}
			for source, ref := range sourceRefs {
				if source.Ref != ref {
					t.Fatal("debug retargeted actual checked relation provenance")
				}
			}
			renderer = nil
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			if err := engine.Reset(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			after, err := retained.Type().Output(context.Background(), retained)
			if err != nil || after != text || rows[0][0] != retained {
				t.Fatal("owned debug output retained mutable graph/catalog storage", err)
			}
		})
	}
}
