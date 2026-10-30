package reference_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwJSONPlan(t *testing.T, p *fixture.Pair, executor fixture.Executor, query string) []map[string]any {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"QUERY PLAN"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "JSON" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing JSON document", rows.Err())
	}
	var text string
	if err := rows.Scan(&text); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("wrong document cardinality", rows.Err())
	}
	var document []map[string]any
	if err := json.Unmarshal([]byte(text), &document); err != nil || len(document) == 0 {
		t.Fatal(text, err)
	}
	var nodes []map[string]any
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		if typ, ok := node["Node Type"].(string); !ok || typ == "" {
			t.Fatal(node)
		}
		nodes = append(nodes, node)
		if children, exists := node["Plans"]; exists {
			for _, child := range children.([]any) {
				visit(child.(map[string]any))
			}
		}
	}
	for _, entry := range document {
		visit(entry["Plan"].(map[string]any))
	}
	return nodes
}

func TestReferenceFDWExplainJSON(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		r.advanced = &fdwAdvancedState{explainProperties: []stuntgres.ForeignExplainProperty{{Name: "same", Value: "first,a:b\"c"}, {Name: "same", Value: "second"}, {Name: "Node Type", Value: "not a structural key"}, {Name: "Plans", Value: "not children"}}}
		fdwPushdownLive(t, p, b, r, run)
		sentinel := fdwExplainPlanningSentinel(run)
		unchanged := func() {
			sentinel()
			fdwWriteSourceRows(t, p, b, r, fixture.Select(`SELECT "Key","Value" FROM fdw_source."Left input" ORDER BY "Key" NULLS LAST,"Value" NULLS LAST`, []string{"Key", "Value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), nil}, {int64(2), int64(20)}, {int64(4), int64(40)}, {nil, int64(30)}}))
			fdwWriteSourceRows(t, p, b, r, fixture.Select(`SELECT "Key","Weight" FROM fdw_source."Right input" ORDER BY "Key" NULLS LAST`, []string{"Key", "Weight"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(1), int64(100)}, {int64(3), int64(300)}, {nil, int64(900)}}))
		}
		for _, fallback := range []bool{false, true} {
			child := "SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
			if fallback {
				child = "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k AND l.v+0<0"
			}
			for _, analyze := range []bool{false, true} {
				prefix := "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) "
				if analyze {
					prefix = "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false,ANALYZE,TIMING false,SUMMARY false) "
				}
				query := prefix + child
				held, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer held.Close()
					for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}} {
						before := r.snapshot()
						nodes := fdwJSONPlan(t, p, executor, query)
						var foreign []map[string]any
						remote := false
						for _, node := range nodes {
							if node["Node Type"] == "Foreign Scan" {
								foreign = append(foreign, node)
							}
							if sql, ok := node["Remote SQL"].(string); ok && sql != "" {
								remote = true
							}
							for _, property := range jsonFDWProperties(node) {
								if property["Name"] == "Remote SQL" && property["Value"] != "" {
									remote = true
								}
							}
							if !analyze {
								for _, key := range []string{"Actual Rows", "Actual Loops", "Actual Total Time"} {
									if node[key] != nil {
										t.Fatal("ordinary metrics", node)
									}
								}
							}
							if !b.PG {
								for _, key := range []string{"Plan Rows", "Logical Cost", "Provider Estimates", "Startup Cost", "Total Cost", "Plan Width", "Actual Startup Time", "Actual Total Time", "Workers", "Shared Hit Blocks", "WAL Records"} {
									if node[key] != nil {
										t.Fatal("unavailable/disabled field", key, node)
									}
								}
							}
						}
						if !remote || len(foreign) == 0 {
							t.Fatal("missing real foreign SQL/plan", nodes)
						}
						if !b.PG {
							if !analyze && r.snapshot().begins != before.begins {
								t.Fatal("ordinary JSON opened foreign execution")
							}
							want := 1
							if fallback {
								want = 2
							}
							if len(foreign) != want {
								t.Fatalf("foreign nodes=%d; want %d: %v", len(foreign), want, nodes)
							}
							for _, node := range foreign {
								properties := jsonFDWProperties(node)
								if len(properties) != 5 || properties[1]["Name"] != "same" || properties[1]["Value"] != "first,a:b\"c" || properties[2]["Name"] != "same" || properties[2]["Value"] != "second" || properties[3]["Name"] != "Node Type" || properties[4]["Name"] != "Plans" {
									t.Fatal("properties lost order/duplicates", properties)
								}
								if !fallback {
									if node["Plans"] != nil || node["Foreign Operation"] != "join" {
										t.Fatal("not selected composite", node)
									}
									sources := node["Foreign Sources"].([]any)
									if len(sources) != 2 {
										t.Fatal(node)
									}
									if sources[0].(map[string]any)["Relation Name"] != "fdw_push_left" || sources[1].(map[string]any)["Relation Name"] != "fdw_push_right" {
										t.Fatal(sources)
									}
									if analyze && (node["Actual Rows"] != float64(4) || node["Actual Loops"] != float64(1)) {
										t.Fatal("actual selected composite counts", node)
									}
								} else if node["Relation Name"] == nil {
									t.Fatal("fallback lost base identity", node)
								}
							}
						}
						unchanged()
					}
				}()
			}
		}
		// Only real selected composite costs exist; never attach base size/width.
		nodes := fdwJSONPlan(t, p, c, "EXPLAIN (FORMAT JSON,VERBOSE) SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k")
		if !b.PG {
			found := false
			for _, node := range nodes {
				if node["Node Type"] != "Foreign Scan" {
					continue
				}
				found = true
				estimates := node["Provider Estimates"].(map[string]any)
				if !reflect.DeepEqual(estimates, map[string]any{"Startup Cost": float64(0), "Total Cost": float64(1)}) {
					t.Fatal(estimates)
				}
				if node["Plan Rows"] != nil || node["Logical Cost"] != nil {
					t.Fatal(node)
				}
			}
			if !found {
				t.Fatal(nodes)
			}
		}
		unchanged()
		text := strings.Join(fdwExplainText(t, p, c, "EXPLAIN (VERBOSE,COSTS false) SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"), "\n")
		if !b.PG && strings.Count(text, "Foreign Scan") != 1 {
			t.Fatal("TEXT selected a different effective tree", text)
		}
	})
}

func jsonFDWProperties(node map[string]any) []map[string]any {
	values, _ := node["Provider Properties"].([]any)
	properties := make([]map[string]any, len(values))
	for i, value := range values {
		properties[i] = value.(map[string]any)
	}
	return properties
}
