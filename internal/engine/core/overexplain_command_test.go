package core_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func explainDebugCommandOwnership(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, sql string
		returning []any
		graph     bool
	}{
		{"literal-insert", "INSERT INTO overexplain_command AS t VALUES(1,'x') RETURNING t.id,t.v", []any{"t.id", "t.v"}, false},
		{"query-insert", "INSERT INTO overexplain_command AS t SELECT s.id,s.v FROM overexplain_command_source s RETURNING t.id,t.v", []any{"t.id", "t.v"}, true},
		{"plain-update", "UPDATE overexplain_command t SET v='x' WHERE id=1 RETURNING t.id,t.v", []any{"t.id", "t.v"}, false},
		{"no-returning", "UPDATE overexplain_command SET v='x' WHERE id=1", []any{}, false},
		{"source-update", "UPDATE overexplain_command t SET v=s.v FROM overexplain_command_source s WHERE t.id=s.id RETURNING s.v,t.id", []any{"s.v", "t.id"}, true},
		{"source-delete", "DELETE FROM overexplain_command t USING overexplain_command_source s WHERE t.id=s.id RETURNING s.v,t.id", []any{"s.v", "t.id"}, true},
		{"source-merge", "MERGE INTO overexplain_command t USING overexplain_command_source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v RETURNING s.v,t.id", []any{"s.v", "t.id"}, true},
		{"returning-subplan", "UPDATE overexplain_command t SET v=s.v FROM overexplain_command_source s WHERE t.id=s.id RETURNING (SELECT s.v)", []any{"(SubPlan 1)"}, true},
	} {
		t.Run("debug-command-"+tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE overexplain_command(id integer,v text)", "CREATE TABLE overexplain_command_source(id integer,v text)"} {
				enginetest.SessionSQL(t, s, q)
			}
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
			if err := scope.RefreshRelationGrantsLocked(e); err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			auth, err := e.StatementAuthorizationLocked(op, statement)
			if err == nil {
				err = scope.CaptureIdentityLocked(auth)
			}
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			// Unlike SELECT, mutation binding requires the exact admitted target;
			// borrow the scope's acquired identity and source-occurrence manifest.
			binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			ref := binding.Ref
			returning := binding.Returning
			var expressions []expr.Expr
			var backing *expr.Expr
			if returning != nil {
				expressions = slices.Clone(returning.Expressions)
				if len(expressions) != 0 {
					backing = &returning.Expressions[0]
				}
			}
			actual := map[plan.Node]bool{}
			collect := func(root plan.Node) {
				plan.VisitExecutableQuery(root, func(n plan.Node) { actual[n] = true }, func(expr.Expr) {})
			}
			if binding.Query != nil {
				collect(binding.Query.Root)
			}
			if binding.Update != nil && binding.Update.From != nil {
				collect(binding.Update.From)
			}
			if binding.Delete.Using != nil {
				collect(binding.Delete.Using)
			}
			if binding.Merge != nil && binding.Merge.Source != nil {
				collect(binding.Merge.Source)
			}
			for _, expression := range expressions {
				plan.WalkExpression(expression, func(n expr.Expr) {
					if subquery, ok := n.(plan.SubqueryExpr); ok && subquery.QueryPlan != nil {
						collect(subquery.QueryPlan.Root)
					}
				})
			}
			host, ok := core.HostOf(scope).(explain.Host)
			if !ok {
				e.Mu.Unlock()
				t.Fatal("actual statement host lacks EXPLAIN authority")
			}
			renderer, err := explain.NewRenderer(host, auth, explain.Options{Debug: true, Format: "json"})
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			_, err = renderer.Command(statement, explain.Bound{Binding: binding})
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			nodes := renderer.PresentedNodes()
			typ, _ := auth.Catalog.LookupType(sqltype.JSON)
			e.Mu.Unlock()
			rows, err := explain.Output(op.Ctx, []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}, explain.Options{Debug: true, Format: "json"}, []*explain.Renderer{renderer}, nil, explain.Summaries{})
			if err != nil {
				t.Fatal(err)
			}
			text, err := rows[0][0].Type().Output(op.Ctx, rows[0][0])
			if err != nil {
				t.Fatal(err)
			}
			var document []map[string]any
			if err := json.Unmarshal([]byte(text), &document); err != nil {
				t.Fatal(err)
			}
			root := document[0]["Plan"].(map[string]any)
			command, ok := root["Embedded Command Debug"].(map[string]any)
			if !ok || command["Relation OID"] != float64(ref.Address.ObjectID) || !reflect.DeepEqual(command["Returning Target List"], tc.returning) {
				t.Fatal("command projection not from actual target/RETURNING owner", command, ref, tc.returning)
			}
			if _, exists := command["Node ID"]; exists {
				t.Fatal("command invented planner node identity")
			}
			if _, exists := root["Embedded Debug"]; exists {
				t.Fatal("synthetic DML wrapper got node ID")
			}
			var projected []map[string]any
			var visit func(map[string]any)
			visit = func(n map[string]any) {
				if debug, exists := n["Embedded Debug"]; exists {
					if n["Node Type"] == "SubPlan" {
						t.Fatal("synthetic subplan wrapper got node ID")
					}
					projected = append(projected, debug.(map[string]any))
				}
				children, _ := n["Plans"].([]any)
				for _, child := range children {
					visit(child.(map[string]any))
				}
			}
			for _, entry := range document {
				visit(entry["Plan"].(map[string]any))
			}
			ids := map[plan.Node]int{}
			var bound []plan.Node
			for _, node := range nodes {
				if actual[node] {
					bound = append(bound, node)
				}
			}
			if len(bound) != len(projected) || (len(bound) != 0) != tc.graph {
				t.Fatal("synthetic candidate adapters gained node IDs or real children lost them", len(bound), len(projected))
			}
			for i, node := range bound {
				id := ids[node]
				if id == 0 {
					id = len(ids) + 1
					ids[node] = id
				}
				if projected[i]["Node ID"] != float64(id) {
					t.Fatal("node identity not from actual DML source/subquery graph", projected[i], id)
				}
			}
			if binding.Ref != ref || binding.Returning != returning {
				t.Fatal("debug mutated actual command target/RETURNING owner")
			}
			if returning != nil && (!reflect.DeepEqual(returning.Expressions, expressions) || backing != nil && &returning.Expressions[0] != backing) {
				t.Fatal("debug mutated actual command expression backing")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
