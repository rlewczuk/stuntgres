package reference_test

import (
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func autoExplainMetric(t *testing.T, b fixture.Backend, document map[string]any, accesses, writes float64) {
	t.Helper()
	root := document["Plan"].(map[string]any)
	if b.PG {
		if _, ok := root["Shared Hit Blocks"]; !ok {
			t.Fatal("real buffer instrumentation absent", document)
		}
		if _, ok := root["WAL Records"]; !ok {
			t.Fatal("real WAL instrumentation absent", document)
		}
		return
	}
	if model, ok := document["Metric Model"].(string); !ok || !strings.Contains(model, "not committed changes") || !strings.Contains(model, "no physical") {
		t.Fatal("logical metric model absent", document)
	}
	if document["Logical Tuple Accesses"] != accesses || document["Logical Tuple Writes"] != writes {
		t.Fatalf("actual-owner logical accesses/writes=%v/%v want %v/%v", document["Logical Tuple Accesses"], document["Logical Tuple Writes"], accesses, writes)
	}
	explainJSONVisit(root, func(node map[string]any) {
		for key := range node {
			if strings.Contains(key, "Blocks") || strings.HasPrefix(key, "WAL ") {
				t.Fatal("fictional physical metric", key, node)
			}
		}
	})
}

func TestReferenceAutoExplainMetrics(t *testing.T) {
	t.Run("duplicate_and_empty_dml_candidates", autoExplainDMLDuplicateCandidates)
	t.Run("demand_and_accepted_writes", func(t *testing.T) {
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ae_metrics(id integer,v text)", 0))
			run(fixture.Exec("INSERT INTO ae_metrics VALUES(1,'one'),(2,NULL),(3,'three')", 3))
			autoExplainConfigure(t, p, b, c, notices)
			run(fixture.Exec("SET auto_explain.log_buffers = on", 0))
			run(fixture.Exec("SET auto_explain.log_wal = on", 0))
			for _, tc := range []struct {
				query  string
				rows   [][]any
				access float64
			}{
				{"SELECT id,v FROM ae_metrics WHERE id<0 ORDER BY id", nil, 3},
				{"SELECT id,v FROM ae_metrics LIMIT 0", nil, 0},
				{"SELECT id,v FROM ae_metrics ORDER BY id LIMIT 1", [][]any{{int64(1), "one"}}, 3},
				{"SELECT id,v FROM ae_metrics ORDER BY id", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}, 3},
				{"WITH x AS MATERIALIZED (SELECT id,v FROM ae_metrics WHERE id<3) SELECT id,v FROM x UNION ALL SELECT id,v FROM x ORDER BY id", [][]any{{int64(1), "one"}, {int64(1), "one"}, {int64(2), nil}, {int64(2), nil}}, 3},
			} {
				run(fixture.Select(tc.query, []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows))
				document := autoExplainJSON(t, notices, tc.query, nil, true, float64(len(tc.rows)))
				autoExplainMetric(t, b, document, tc.access, 0)
				if strings.HasPrefix(tc.query, "WITH ") {
					var cte map[string]any
					if b.PG {
						cte = explainJSONFind(t, explainJSONDocument{document}, "Subplan Name", "CTE x")
					} else {
						producers, ok := document["Rewritten Plans"].([]any)
						if !ok || len(producers) != 1 {
							t.Fatal("missing single genuine materialized producer", document)
						}
						cte, ok = producers[0].(map[string]any)
						if !ok || cte["Node Type"] != "CTE" || cte["CTE Name"] != "x" {
							t.Fatal("materialized producer wrapper lost its presentation", cte)
						}
					}
					if cte["Actual Rows"] != float64(2) || cte["Actual Loops"] != float64(1) {
						t.Fatal("materialized producer actuals must not count repeated consumer output", cte)
					}
					if !b.PG {
						child := cte["Plans"].([]any)[0].(map[string]any)
						if child["Actual Rows"] != float64(2) || child["Actual Loops"] != float64(1) {
							t.Fatal("CTE wrapper must borrow its real producer measurements", cte)
						}
					}
				}
			}
			for _, tc := range []struct {
				query  string
				count  int64
				writes float64
			}{
				{"INSERT INTO ae_metrics VALUES(4,'four'),(5,'five')", 2, 2},
				{"UPDATE ae_metrics SET v='changed' WHERE id=4", 1, 1},
				{"DELETE FROM ae_metrics WHERE id=5", 1, 1},
			} {
				run(fixture.Prepared(fixture.Exec(tc.query, tc.count)))
				document := autoExplainJSON(t, notices, tc.query, nil, true, 0)
				if !b.PG && strings.HasPrefix(tc.query, "INSERT ") {
					autoExplainDMLNode(t, document["Plan"].(map[string]any), "Values", 2, 1)
					autoExplainMetric(t, b, document, 0, 2)
				}
				if b.PG {
					autoExplainMetric(t, b, document, 0, tc.writes)
				} else if document["Logical Tuple Writes"] != tc.writes {
					t.Fatal("accepted stage writes", document)
				}
			}
			run(fixture.Exec("SET auto_explain.log_analyze = off", 0))
			const query = "SELECT id,v FROM ae_metrics WHERE id=4 ORDER BY id"
			run(fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "changed"}}))
			document := autoExplainJSON(t, notices, query, nil, false, 0)
			for _, key := range []string{"Logical Tuple Accesses", "Logical Tuple Writes", "Triggers"} {
				if _, ok := document[key]; ok {
					t.Fatal("ANALYZE gating", document)
				}
			}
		})
	})
	t.Run("trigger_identity_and_effects_once", func(t *testing.T) {
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TABLE ae_trigger(id integer)", "CREATE TABLE ae_audit(id integer, name text)",
				"CREATE FUNCTION ae_trigger_body() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ae_audit VALUES(NEW.id,TG_NAME); RETURN NEW; END $$",
				"CREATE TRIGGER ae_first AFTER INSERT ON ae_trigger FOR EACH ROW EXECUTE FUNCTION ae_trigger_body()",
				"CREATE TRIGGER ae_second AFTER INSERT ON ae_trigger FOR EACH ROW EXECUTE FUNCTION ae_trigger_body()",
			} {
				run(fixture.Exec(q, 0))
			}
			autoExplainConfigure(t, p, b, c, notices)
			for i, tc := range []struct {
				name                               string
				triggers, nested, analyzed, timing bool
			}{
				{name: "disabled", analyzed: true},
				{name: "calls_without_timing", triggers: true, analyzed: true},
				{name: "nested_audit", triggers: true, nested: true, analyzed: true},
				{name: "analyze_off", triggers: true, nested: true},
				{name: "timed_invocations", triggers: true, nested: true, analyzed: true, timing: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, setting := range []struct {
						name  string
						value bool
					}{
						{"log_triggers", tc.triggers},
						{"log_nested_statements", tc.nested},
						{"log_analyze", tc.analyzed},
						{"log_timing", tc.timing},
					} {
						value := "off"
						if setting.value {
							value = "on"
						}
						run(fixture.Exec("SET auto_explain."+setting.name+" = "+value, 0))
					}
					const query = "INSERT INTO ae_trigger VALUES(1),(2)"
					run(fixture.Exec("SET auto_explain.log_min_duration = 0", 0))
					run(fixture.Exec(query, 2))
					document := autoExplainTriggerPlans(t, b, notices, query, tc.analyzed, tc.timing, tc.nested)
					value, present := document["Triggers"]
					if !tc.triggers || !tc.analyzed {
						if present {
							t.Fatal("disabled trigger metrics or ANALYZE gating", document)
						}
					} else {
						entries, ok := value.([]any)
						if !ok || len(entries) != 2 {
							t.Fatal("two distinct trigger identities", document)
						}
						seen := map[string]bool{}
						for _, raw := range entries {
							entry, ok := raw.(map[string]any)
							if !ok {
								t.Fatal("trigger entry is not an object", raw)
							}
							name, ok := entry["Trigger Name"].(string)
							if !ok || seen[name] || name != "ae_first" && name != "ae_second" || entry["Calls"] != float64(2) || entry["Relation"] != "ae_trigger" {
								t.Fatal("actual trigger identity/calls", entry)
							}
							seen[name] = true
							elapsed, present := entry["Time"]
							if tc.timing {
								autoExplainTriggerNumber(t, elapsed, "trigger Time", entry)
							} else if present {
								t.Fatal("timing off retains calls only", entry)
							}
						}
					}
					// Check each completed mutation's cumulative effects independently
					// of log order, including when ANALYZE or trigger metrics are off.
					run(fixture.Exec("SET auto_explain.log_min_duration = -1", 0))
					var audit [][]any
					for _, id := range []int64{1, 2} {
						for _, name := range []string{"ae_first", "ae_second"} {
							for invocation := 0; invocation <= i; invocation++ {
								audit = append(audit, []any{id, name})
							}
						}
					}
					run(fixture.Select("SELECT id,name FROM ae_audit ORDER BY id,name", []string{"id", "name"}, []string{"INT4", "TEXT"}, audit))
					autoExplainQuiet(t, notices)
				})
			}
		})
	})
}

// Trigger-body executor completions form a source/count bag: two real calls of
// each trigger yield four audit INSERT plans, never an assumed delivery order.
func autoExplainTriggerPlans(t *testing.T, b fixture.Backend, notices *[]fixture.Diagnostic, query string, analyzed, timing, nested bool) map[string]any {
	t.Helper()
	if !nested && !timing {
		return autoExplainJSON(t, notices, query, nil, analyzed, 0)
	}
	const innerSource = "INSERT INTO ae_audit VALUES(NEW.id,TG_NAME)"
	const innerContext = "SQL statement \"" + innerSource + "\"\nPL/pgSQL function ae_trigger_body() line 1 at SQL statement"
	wantInner := 0
	if nested {
		wantInner = 4
	}
	if len(*notices) != 1+wantInner {
		t.Fatalf("trigger completion bag wants outer1/audit%d, got %#v", wantInner, *notices)
	}
	outerCount, innerCount := 0, 0
	var outer map[string]any
	for len(*notices) != 0 {
		one := []fixture.Diagnostic{(*notices)[0]}
		*notices = (*notices)[1:]
		body := strings.TrimPrefix(one[0].Message, autoExplainDuration.FindString(one[0].Message))
		var document map[string]any
		if err := json.Unmarshal([]byte(body), &document); err != nil || document == nil {
			t.Fatalf("trigger plan must be a JSON object: %q: %v", body, err)
		}
		context := ""
		switch document["Query Text"] {
		case query:
			outerCount++
			outer = document
			if _, present := document["Query Parameters"]; present {
				t.Fatal("unparameterized outer trigger mutation", document)
			}
		case innerSource:
			innerCount++
			context = innerContext
		default:
			t.Fatal("wrong genuine trigger executor source", document)
		}
		autoExplainMessage(t, &one, "NOTICE", context)
		root, ok := document["Plan"].(map[string]any)
		if !ok {
			t.Fatal("trigger executor Plan absent", document)
		}
		explainJSONVisit(root, func(node map[string]any) {
			if typ, ok := node["Node Type"].(string); !ok || typ == "" {
				t.Fatal("trigger executor Node Type absent", node)
			}
			if !analyzed {
				for key := range node {
					if strings.HasPrefix(key, "Actual ") {
						t.Fatal("analyze off exposes node actuals", node)
					}
				}
				return
			}
			for _, key := range []string{"Actual Rows", "Actual Loops"} {
				autoExplainTriggerNumber(t, node[key], key, node)
			}
			for _, key := range []string{"Actual Startup Time", "Actual Total Time"} {
				value, present := node[key]
				if key == "Actual Startup Time" && !b.PG {
					if present {
						t.Fatal("native active-elapsed model invented startup timing", node)
					}
					continue
				}
				if timing {
					autoExplainTriggerNumber(t, value, key, node)
				} else if present {
					t.Fatal("timing off exposes node clocks", node)
				}
			}
		})
		if analyzed && (root["Actual Rows"] != float64(0) || root["Actual Loops"] != float64(1)) {
			t.Fatal("real no-RETURNING trigger/audit mutation root must be 0/1", document)
		}
		if !analyzed {
			for _, key := range []string{"Triggers", "Logical Tuple Accesses", "Logical Tuple Writes"} {
				if _, present := document[key]; present {
					t.Fatal("analyze off exposes instrumentation", document)
				}
			}
		}
	}
	if outerCount != 1 || innerCount != wantInner {
		t.Fatal("wrong trigger source/count bag", outerCount, innerCount, wantInner)
	}
	return outer
}

func autoExplainTriggerNumber(t *testing.T, value any, field string, owner map[string]any) {
	t.Helper()
	number, ok := value.(float64)
	if !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		t.Fatalf("real %s must be finite and nonnegative: %#v", field, owner)
	}
}
