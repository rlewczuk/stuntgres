package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func explainJSONCommandProperties(t *testing.T) {
	cases := []struct {
		name, sql, node, field string
		count                  int
	}{
		{"defaults", "INSERT INTO ej_target DEFAULT VALUES RETURNING id,v", "Insert", "Defaults", 3},
		{"targets", "INSERT INTO ej_target(id,v) VALUES(4,40),(5,50) RETURNING id,v", "Insert", "Target Columns", 2},
		{"assignments", "UPDATE ej_target SET v=v+1,a[1]=9 WHERE id=1 RETURNING old.v,v,a", "Update", "Assignments", 2},
		{"assignment-default", "UPDATE ej_default_target SET v=DEFAULT WHERE v=41 RETURNING v", "Update", "Defaults", 1},
		{"from", "UPDATE ej_target t SET v=s.v FROM ej_source s WHERE t.id=s.id RETURNING t.id,s.v", "Update", "Returning", 2},
		{"delete", "DELETE FROM ej_target t USING ej_source s WHERE t.id=s.id RETURNING t.id,s.v", "Delete", "Returning", 2},
		{"conflict", "INSERT INTO ej_target(id,v) VALUES(1,99) ON CONFLICT(id) WHERE id>0 DO UPDATE SET v=excluded.v WHERE excluded.v>ej_target.v RETURNING id,v", "Insert", "Conflict Arbiter", 1},
		{"constraint", "INSERT INTO ej_target(id,v) VALUES(1,99) ON CONFLICT ON CONSTRAINT ej_target_pkey DO NOTHING", "Insert", "", 0},
		{"merge", "MERGE INTO ej_target t USING ej_source s ON t.id=s.id WHEN MATCHED AND s.v>0 THEN UPDATE SET v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY TARGET AND s.id>0 THEN INSERT(id,v) VALUES(s.id,s.v) WHEN NOT MATCHED BY SOURCE AND t.id>0 THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DO NOTHING RETURNING t.id", "Merge", "Merge Arms", 5},
		{"cte", "WITH changed AS (UPDATE ej_target SET v=99 WHERE id=1 RETURNING id) DELETE FROM ej_target WHERE id IN(SELECT id FROM changed) RETURNING id", "Delete", "Returning", 1},
		{"group-assignment", "UPDATE ej_target SET (id,v)=(SELECT id,v+1 FROM ej_source WHERE id=1) WHERE id=1", "Update", "Assignments", 2},
		{"view", "UPDATE ej_view SET v=88 WHERE id=1 RETURNING id", "Update", "Returning", 1},
		{"rule", "INSERT INTO ej_rule VALUES(8)", "Rule Rewrite", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
					for _, sql := range []string{
						"CREATE SEQUENCE ej_sequence START 1",
						"CREATE TABLE ej_target(id integer PRIMARY KEY DEFAULT nextval('ej_sequence'),v integer DEFAULT 7,a integer[] DEFAULT ARRAY[1,2])",
						"CREATE TABLE ej_source(id integer,v integer)",
						"CREATE VIEW ej_view AS SELECT id,v FROM ej_target",
						"CREATE TABLE ej_rule(id integer)", "CREATE TABLE ej_log(id integer)",
						"CREATE RULE ej_copy AS ON INSERT TO ej_rule DO ALSO INSERT INTO ej_log VALUES(NEW.id)",
					} {
						run(fixture.Exec(sql, 0))
					}
					run(fixture.Exec("INSERT INTO ej_target(id,v) VALUES(1,10),(2,20),(3,30)", 3))
					run(fixture.Exec("INSERT INTO ej_source VALUES(1,11),(4,44)", 2))
					if tc.name == "assignment-default" {
						run(fixture.Exec("CREATE DOMAIN ej_default_value AS integer DEFAULT nextval('ej_sequence')", 0))
						run(fixture.Exec("CREATE TABLE ej_default_target(v ej_default_value)", 0))
						run(fixture.Exec("INSERT INTO ej_default_target VALUES(41)", 1))
					}
					explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) "+tc.sql, func(document explainJSONDocument, _ string) {
						explainJSONOrdinary(t, b, document)
						if b.PG {
							target := "ej_target"
							if tc.name == "rule" {
								target = "ej_log"
							}
							if tc.name == "assignment-default" {
								target = "ej_default_target"
							}
							explainJSONFind(t, document, "Relation Name", target)
						} else {
							node := explainJSONFind(t, document, "Node Type", tc.node)
							if tc.field != "" {
								explainJSONList(t, node, tc.field, tc.count)
							}
							switch tc.name {
							case "defaults":
								defaults := explainJSONList(t, node, "Defaults", 3)
								for i, name := range []string{"id", "v", "a"} {
									item := defaults[i].(map[string]any)
									if item["Column"] != name || item["Expression"] == nil {
										t.Fatal(defaults)
									}
								}
								if node["Default Values"] != true {
									t.Fatal(node)
								}
							case "targets":
								if !reflect.DeepEqual(node["Target Columns"], []any{"id", "v"}) {
									t.Fatal(node)
								}
							case "assignments":
								assignments := explainJSONList(t, node, "Assignments", 2)
								if assignments[0].(map[string]any)["Column"] != "v" || assignments[1].(map[string]any)["Column"] != "a" || assignments[1].(map[string]any)["Target"] != "a[1]" {
									t.Fatal(assignments)
								}
								explainJSONContains(t, node["Returning"], "old.v", "ej_target.v")
							case "assignment-default":
								defaults := explainJSONList(t, node, "Defaults", 1)
								definition := defaults[0].(map[string]any)
								if definition["Column"] != "v" || definition["Expression"] == nil {
									t.Fatal(defaults)
								}
								explainJSONContains(t, definition["Expression"], "nextval", "ej_sequence")
								assignment := explainJSONList(t, node, "Assignments", 1)[0].(map[string]any)
								if assignment["Column"] != "v" || assignment["Expression"] != definition["Expression"] {
									t.Fatal(assignment)
								}
							case "conflict":
								if node["Conflict Action"] != "DO UPDATE" || node["Conflict Predicate"] == nil || node["Action Filter"] == nil {
									t.Fatal(node)
								}
								assignment := explainJSONList(t, node, "Assignments", 1)[0].(map[string]any)
								if !strings.Contains(assignment["Expression"].(string), "excluded.v") {
									t.Fatal(assignment)
								}
							case "constraint":
								if node["Conflict Constraint"] != "ej_target_pkey" || node["Conflict Action"] != "DO NOTHING" {
									t.Fatal(node)
								}
							case "merge":
								arms := explainJSONList(t, node, "Merge Arms", 5)
								for i, action := range []string{"UPDATE", "DELETE", "INSERT", "DELETE", "DO NOTHING"} {
									if arms[i].(map[string]any)["Action"] != action {
										t.Fatal(arms)
									}
								}
								first, insert, last := arms[0].(map[string]any), arms[2].(map[string]any), arms[4].(map[string]any)
								if first["Match"] != "MATCHED" || first["Condition"] == nil || last["Match"] != "NOT MATCHED BY SOURCE" {
									t.Fatal(arms)
								}
								explainJSONList(t, first, "Assignments", 1)
								explainJSONList(t, insert, "Insert Values", 1)
								if !reflect.DeepEqual(insert["Target Columns"], []any{"id", "v"}) {
									t.Fatal(insert)
								}
								if last["Assignments"] != nil || last["Insert Values"] != nil {
									t.Fatal("arm details leaked", last)
								}
							case "cte":
								explainJSONFind(t, document, "Node Type", "CTE")
								explainJSONFind(t, document, "Node Type", "Update")
								explainJSONFind(t, document, "Node Type", "SubPlan")
							case "group-assignment":
								explainJSONFind(t, document, "Node Type", "SubPlan")
							case "view":
								writable := node["Writable View"].(map[string]any)
								if writable["Relation Name"] != "ej_view" || writable["Schema"] != "public" {
									t.Fatal(node)
								}
							case "rule":
								children := explainJSONList(t, node, "Plans", 2)
								for _, child := range children {
									if child.(map[string]any)["Rule Rewrite Command"] != true {
										t.Fatal(child)
									}
								}
							}
						}
						run(fixture.Select("SELECT id,v,a[1] AS first FROM ej_target ORDER BY id", []string{"id", "v", "first"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(20), int64(1)}, {int64(3), int64(30), int64(1)}}))
						run(fixture.Select("SELECT id,v FROM ej_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(4), int64(44)}}))
						run(fixture.Select("SELECT id FROM ej_rule", []string{"id"}, []string{"INT4"}, nil))
						run(fixture.Select("SELECT id FROM ej_log", []string{"id"}, []string{"INT4"}, nil))
						if tc.name == "assignment-default" {
							run(fixture.Select("SELECT v::integer AS v FROM ej_default_target", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
						}
						run(fixture.Select("SELECT last_value,is_called FROM ej_sequence", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
					})
				})
			}
		})
	}
}
