package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func explainJSONLogicalProperties(t *testing.T) {
	cases := []struct {
		name, sql, node, field string
		value                  any
		list                   int
		pg                     string
	}{
		{"singleton", "SELECT 42 AS answer", "Singleton", "", nil, 0, "42"},
		{"filter", "SELECT id FROM explain_json WHERE id>1", "Filter", "Filter", nil, 0, "explain_json"},
		{"sort", "SELECT id FROM explain_json ORDER BY id DESC", "Sort", "Sort Key", nil, 1, "Sort Key"},
		{"evaluate", "SELECT id,random() FROM explain_json ORDER BY id LIMIT 1", "Evaluate", "Expressions", nil, 1, "random"},
		{"aggregate", "SELECT v,count(*),sum(id) FROM explain_json GROUP BY v HAVING count(*)>1", "Aggregate", "Aggregate", nil, 2, "count(*)"},
		{"sets", "SELECT v,count(*) FROM explain_json GROUP BY GROUPING SETS ((v),())", "Aggregate", "Grouping Sets", nil, 2, "count(*)"},
		{"distinct", "SELECT DISTINCT v FROM explain_json", "Distinct", "Distinct Key", nil, 1, "explain_json"},
		{"distinct-on", "SELECT DISTINCT ON(v) v,id FROM explain_json ORDER BY v,id DESC", "Distinct On", "Distinct On Key", nil, 1, "explain_json"},
		{"join", "SELECT a.id,b.id FROM explain_json a LEFT JOIN explain_json b ON a.id=b.id", "Join", "Join Type", "Left", 0, "explain_json"},
		{"cross", "SELECT a.id,b.id FROM explain_json a CROSS JOIN explain_json b", "Join", "Join Type", "Cross", 0, "explain_json"},
		{"derived", "SELECT x.id FROM (SELECT id FROM explain_json) x", "Derived Scan", "Alias", "x", 0, "explain_json"},
		{"cte", "WITH x AS MATERIALIZED (SELECT id FROM explain_json) SELECT id FROM x", "CTE", "CTE Name", "x", 0, "explain_json"},
		{"recursive", "WITH RECURSIVE walk(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM walk WHERE n<3) SEARCH DEPTH FIRST BY n SET seq CYCLE n SET cyc USING path SELECT n,seq,cyc,path FROM walk", "Recursive Union", "All", true, 0, "Recursive Union"},
		{"union", "SELECT id FROM explain_json UNION ALL SELECT id FROM explain_json", "Set", "Operation", "UNION", 0, "explain_json"},
		{"intersect", "VALUES(1),(2) INTERSECT ALL VALUES(2),(3)", "Set", "Operation", "INTERSECT", 0, "Node Type"},
		{"except", "VALUES(1),(2) EXCEPT VALUES(2),(3)", "Set", "Operation", "EXCEPT", 0, "Node Type"},
		{"limit", "SELECT id FROM explain_json ORDER BY id OFFSET 1 LIMIT 2", "Limit", "Count", nil, 0, "explain_json"},
		{"ties", "SELECT id FROM explain_json ORDER BY id FETCH FIRST 1 ROW WITH TIES", "Limit", "With Ties", true, 0, "explain_json"},
		{"lock", "SELECT id FROM explain_json FOR UPDATE OF explain_json NOWAIT", "Row Lock", "Row Marks", nil, 1, "LockRows"},
		{"window", "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM explain_json", "Window", "Window", nil, 1, "row_number() OVER"},
		{"function", "SELECT g.n,g.ord FROM generate_series(1,3) WITH ORDINALITY g(n,ord)", "Function Scan", "Functions", nil, 1, "generate_series"},
		{"lateral", "SELECT a.id,x.v FROM explain_json a CROSS JOIN LATERAL generate_series(1,a.id+1) x(v)", "Function Scan", "Lateral", "correlated function input", 0, "generate_series"},
		{"set-project", "SELECT id,generate_series(1,2) FROM explain_json", "Set Project", "Functions", nil, 1, "generate_series"},
		{"subplan", "SELECT a.id,(SELECT max(b.id) FROM explain_json b WHERE b.v=a.v) FROM explain_json a", "SubPlan", "Subplan Name", "SubPlan 1", 0, "SubPlan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					explainJSONSetup(t, p, b)
					explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) "+tc.sql, func(document explainJSONDocument, _ string) {
						explainJSONOrdinary(t, b, document)
						if b.PG {
							explainJSONContains(t, document, tc.pg)
							return
						}
						node := explainJSONFind(t, document, "Node Type", tc.node)
						if tc.field != "" {
							if _, exists := node[tc.field]; !exists {
								t.Fatalf("missing %s: %v", tc.field, node)
							}
							if tc.value != nil && !reflect.DeepEqual(node[tc.field], tc.value) {
								t.Fatalf("%s=%v; want %v", tc.field, node[tc.field], tc.value)
							}
							if tc.list > 0 {
								explainJSONList(t, node, tc.field, tc.list)
							}
						}
						switch tc.name {
						case "sets":
							sets := explainJSONList(t, node, "Grouping Sets", 2)
							if len(sets[0].([]any)) != 1 || len(sets[1].([]any)) != 0 {
								t.Fatal(sets)
							}
						case "recursive":
							search := node["Search"].(map[string]any)
							if search["Direction"] != "DEPTH FIRST" || search["Sequence"] != "seq" || !reflect.DeepEqual(search["Keys"], []any{"n"}) {
								t.Fatal(search)
							}
							cycle := node["Cycle"].(map[string]any)
							if cycle["Mark"] != "cyc" || cycle["Path"] != "path" || !reflect.DeepEqual(cycle["Keys"], []any{"n"}) {
								t.Fatal(cycle)
							}
							explainJSONFind(t, document, "Node Type", "Worktable Scan")
						case "lock":
							mark := explainJSONList(t, node, "Row Marks", 1)[0].(map[string]any)
							if mark["Mode"] != "FOR UPDATE" || mark["Wait"] != "NOWAIT" || mark["Alias"] != "explain_json" {
								t.Fatal(mark)
							}
						case "function":
							if node["Ordinality"] != true {
								t.Fatal(node)
							}
						case "limit":
							if node["Offset"] == nil {
								t.Fatal(node)
							}
						case "join":
							if node["Join Condition"] == nil || node["Method"] == nil {
								t.Fatal(node)
							}
						}
					})
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
				})
			}
		})
	}
	t.Run("raw-identifiers", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE "Json,Table:X"("Odd:Column" integer)`, 0))
				query := "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) SELECT \"Odd,Alias:\nX\".\"Odd:Column\" FROM \"Json,Table:X\" AS \"Odd,Alias:\nX\""
				explainJSONRoutes(t, p, b, query, func(document explainJSONDocument, _ string) {
					scan := explainJSONFind(t, document, "Relation Name", "Json,Table:X")
					if scan["Alias"] != "Odd,Alias:\nX" || scan["Schema"] != "public" {
						t.Fatal(scan)
					}
					explainJSONList(t, document[0]["Plan"].(map[string]any), "Output", 1)
				})
			})
		}
	})
}
