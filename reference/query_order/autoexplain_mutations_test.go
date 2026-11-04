package reference_test

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"stuntgres/reference/fixture"
)

func autoExplainMutationCases(t *testing.T) {
	t.Run("dml_role_formats", autoExplainDMLRoleFormats)
	t.Run("dml_input_and_empty_merge", autoExplainDMLInputBoundaries)
	t.Run("dml_cte_current_presentation", autoExplainDMLCTEPresentation)
	t.Run("merge_cte_insert_select_and_rewrite", func(t *testing.T) {
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ae_merge(id integer PRIMARY KEY,v integer)", 0))
			run(fixture.Exec("INSERT INTO ae_merge VALUES(1,10),(2,20)", 2))
			run(fixture.Exec("CREATE TABLE ae_rewrite(id integer)", 0))
			run(fixture.Exec("CREATE TABLE ae_rewrite_audit(id integer)", 0))
			run(fixture.Exec("CREATE RULE ae_rewrite_rule AS ON INSERT TO ae_rewrite DO ALSO INSERT INTO ae_rewrite_audit VALUES(NEW.id)", 0))
			autoExplainConfigure(t, p, b, c, notices)
			const merge = "MERGE INTO ae_merge t USING (VALUES(1,5),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)"
			count := int64(2)
			if b.PG {
				count = 0
			} // lib/pq's established MERGE tag boundary.
			run(fixture.Prepared(fixture.Exec(merge, count)))
			document := autoExplainJSON(t, notices, merge, nil, true, 0)
			if !b.PG {
				autoExplainDMLNode(t, document["Plan"].(map[string]any), "Join", 2, 1)
				autoExplainDMLNode(t, document["Plan"].(map[string]any), "Logical Scan", 2, 2)
				autoExplainDMLNode(t, document["Plan"].(map[string]any), "Values", 2, 1)
			}
			const contents = "SELECT id,v FROM ae_merge ORDER BY id"
			run(fixture.Select(contents, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(15)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
			autoExplainJSON(t, notices, contents, nil, true, 3)
			const insertSelect = "INSERT INTO ae_merge SELECT 4,40"
			run(fixture.Exec(insertSelect, 1))
			autoExplainJSON(t, notices, insertSelect, nil, true, 0)
			const cte = "WITH changed AS (UPDATE ae_merge SET v=v+1 WHERE id=4 RETURNING id,v) SELECT id,v FROM changed"
			run(fixture.Prepared(fixture.Select(cte, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(41)}})))
			autoExplainJSON(t, notices, cte, nil, true, 1)
			const rewrite = "INSERT INTO ae_rewrite VALUES(7)"
			run(fixture.Exec(rewrite, 1))
			// Genuine rule queries can have separate executor completions on the
			// oracle. Native carries the genuine rewrite renderers in one frame.
			if b.PG {
				if len(*notices) != 2 {
					t.Fatal("two actual rule executors", *notices)
				}
				for len(*notices) != 0 {
					one := []fixture.Diagnostic{(*notices)[0]}
					*notices = (*notices)[1:]
					autoExplainMessage(t, &one, "NOTICE")
				}
			} else {
				autoExplainJSON(t, notices, rewrite, nil, true, 0)
			}
			run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
			run(fixture.Select("SELECT id FROM ae_rewrite_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT id FROM ae_rewrite ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("SET auto_explain.log_min_duration=0", 0))
			run(fixture.Exec("SET auto_explain.log_wal=on", 0))
			const conflict = "INSERT INTO ae_merge VALUES(1,99) ON CONFLICT DO NOTHING"
			run(fixture.Prepared(fixture.Exec(conflict, 0)))
			document = autoExplainJSON(t, notices, conflict, nil, true, 0)
			if !b.PG {
				autoExplainDMLNode(t, document["Plan"].(map[string]any), "Values", 1, 1)
			}
			if !b.PG && document["Logical Tuple Writes"] != float64(0) {
				t.Fatal("rejected conflict recorded an accepted write", document)
			}
		})
	})
}

// Native topology is intentionally not PostgreSQL's physical topology. These
// assertions specify real DML producer work independently of command effects.
func autoExplainDMLNode(t *testing.T, root map[string]any, nodeType string, rows, loops float64) map[string]any {
	t.Helper()
	check := func(node map[string]any) {
		if node["Actual Rows"] != rows || node["Actual Loops"] != loops {
			t.Fatalf("%s actual rows/loops=%v/%v want %v/%v", nodeType, node["Actual Rows"], node["Actual Loops"], rows, loops)
		}
	}
	if mutation := root["Node Type"]; nodeType == "Filter" && (mutation == "Update" || mutation == "Delete") {
		// The mutation owns this immediate child. A FROM/USING source can
		// contain independent filters with different demand and loop counts.
		children, ok := root["Plans"].([]any)
		if !ok || len(children) != 1 {
			t.Fatalf("want exactly one mutation action input: %v", root)
		}
		node, ok := children[0].(map[string]any)
		condition, action := root["Action Filter"]
		if !ok || node["Node Type"] != "Filter" || action && node["Filter"] != condition {
			t.Fatalf("mutation action filter does not own its immediate input/condition %v: %v", condition, root)
		}
		check(node)
		return node
	}
	var found []map[string]any
	explainJSONVisit(root, func(node map[string]any) {
		if node["Node Type"] == nodeType {
			found = append(found, node)
			check(node)
		}
	})
	if len(found) != 1 {
		t.Fatalf("want exactly one %s role, found %d: %v", nodeType, len(found), root)
	}
	return found[0]
}

func autoExplainDMLInputBoundaries(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, query := range []string{
			"CREATE SEQUENCE ae_input_once",
			"CREATE TABLE ae_input_default(id bigint DEFAULT nextval('ae_input_once'))",
			"CREATE TABLE ae_input_suppressed(id integer)",
			"CREATE FUNCTION ae_input_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE TRIGGER ae_input_suppress BEFORE INSERT ON ae_input_suppressed FOR EACH ROW EXECUTE FUNCTION ae_input_suppress()",
			"CREATE TABLE ae_input_params(id integer)",
			"CREATE TABLE ae_input_merge(id integer,v integer)",
		} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("INSERT INTO ae_input_merge VALUES(1,10),(2,20)", 2))
		autoExplainConfigure(t, p, b, c, notices)
		run(fixture.Exec("SET auto_explain.log_buffers=on", 0))
		run(fixture.Exec("SET auto_explain.log_wal=on", 0))
		for _, tc := range []struct {
			query, input string
			count        int64
		}{
			// The accepted DEFAULT VALUES binding owns one expression row;
			// the shared renderer therefore labels its input Values.
			{"INSERT INTO ae_input_default DEFAULT VALUES", "Values", 1},
			{"INSERT INTO ae_input_suppressed VALUES(1)", "Values", 0},
		} {
			run(fixture.Prepared(fixture.Exec(tc.query, tc.count)))
			document := autoExplainJSON(t, notices, tc.query, nil, true, 0)
			autoExplainMetric(t, b, document, 0, float64(tc.count))
			if !b.PG {
				autoExplainDMLNode(t, document["Plan"].(map[string]any), tc.input, 1, 1)
			}
		}
		const insert = "INSERT INTO ae_input_params VALUES($1)"
		held, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, argument := range []int64{1, 2, 99, 1} {
			result, err := held.ExecContext(p.Ctx, argument)
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal("current prepared insertion", count, err)
			}
			parameters := fmt.Sprintf("$1 = '%d'", argument)
			document := autoExplainJSON(t, notices, insert, &parameters, true, 0)
			autoExplainMetric(t, b, document, 0, 1)
			if !b.PG {
				autoExplainDMLNode(t, document["Plan"].(map[string]any), "Values", 1, 1)
			}
		}
		for _, bySource := range []bool{false, true} {
			query := "MERGE INTO ae_input_merge t USING (SELECT 1 AS id WHERE false) s ON t.id=s.id WHEN MATCHED THEN DO NOTHING"
			targetRows, targetLoops, pairs := float64(0), float64(0), float64(0)
			if bySource {
				query += " WHEN NOT MATCHED BY SOURCE THEN DO NOTHING"
				targetRows, targetLoops, pairs = 2, 1, 2
			}
			run(fixture.Prepared(fixture.Exec(query, 0)))
			document := autoExplainJSON(t, notices, query, nil, true, 0)
			autoExplainMetric(t, b, document, targetRows, 0)
			if !b.PG {
				root := document["Plan"].(map[string]any)
				autoExplainDMLNode(t, root, "Logical Scan", targetRows, targetLoops)
				autoExplainDMLNode(t, root, "Join", pairs, 1)
			}
		}
		run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
		run(fixture.Select("SELECT id FROM ae_input_params ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(99)}}))
		run(fixture.Select("SELECT id FROM ae_input_default", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT last_value,is_called FROM ae_input_once", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
		run(fixture.Select("SELECT id FROM ae_input_suppressed", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT id,v FROM ae_input_merge ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		autoExplainQuiet(t, notices)
	})
}

func autoExplainDMLRoleFormats(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, format := range []string{"text", "xml", "json", "yaml"} {
			run(fixture.Exec("CREATE TABLE ae_role_insert_"+format+"(id integer)", 0))
			run(fixture.Exec("CREATE TABLE ae_role_merge_"+format+"(id integer,v integer)", 0))
			run(fixture.Exec("INSERT INTO ae_role_merge_"+format+" VALUES(1,10),(2,20)", 2))
		}
		autoExplainConfigure(t, p, b, c, notices)
		for _, format := range []string{"text", "xml", "json", "yaml"} {
			run(fixture.Exec("SET auto_explain.log_format="+format, 0))
			insert := "INSERT INTO ae_role_insert_" + format + " VALUES(1),(2)"
			merge := "MERGE INTO ae_role_merge_" + format + " t USING (VALUES(1,5),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)"
			for _, query := range []string{insert, merge} {
				count := int64(2)
				if b.PG && query == merge {
					count = 0 // Existing lib/pq MERGE command-tag boundary.
				}
				run(fixture.Prepared(fixture.Exec(query, count)))
				body := autoExplainMessage(t, notices, "NOTICE")
				if format == "text" {
					if !strings.Contains(body, "Query Text: "+query) || !strings.Contains(body, "actual") {
						t.Fatal("actual text/source", body)
					}
					if !regexp.MustCompile(`(?m)^(?:Insert|Merge)[^\n]*\(actual rows=0(?:\.00)? loops=1\)`).MatchString(body) {
						t.Fatal("text no-return command root actual", body)
					}
					if !b.PG {
						patterns := []string{`Values[^\n]*\(actual rows=2\.00 loops=1\)`}
						if query == merge {
							patterns = append(patterns, `Join[^\n]*\(actual rows=2\.00 loops=1\)`, `Logical Scan[^\n]*\(actual rows=2\.00 loops=2\)`)
						}
						for _, pattern := range patterns {
							if !regexp.MustCompile(pattern).MatchString(body) {
								t.Fatal("text DML role actual", pattern, body)
							}
						}
					}
					continue
				}
				root := autoExplainDMLFormatRoot(t, body, format)
				explainJSONVisit(root, func(node map[string]any) {
					for _, key := range []string{"Actual Rows", "Actual Loops"} {
						if value, ok := node[key].(float64); !ok || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
							t.Fatal("recursive DML actual absent", format, node)
						}
					}
					if _, timed := node["Actual Total Time"]; timed {
						t.Fatal("timing-off DML clock", node)
					}
				})
				if root["Actual Rows"] != float64(0) || root["Actual Loops"] != float64(1) {
					t.Fatal("no-return command root is independent of input/writes", root)
				}
				if !b.PG {
					autoExplainDMLNode(t, root, "Values", 2, 1)
					if query == merge {
						autoExplainDMLNode(t, root, "Join", 2, 1)
						autoExplainDMLNode(t, root, "Logical Scan", 2, 2)
					}
				}
			}
		}
		run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
		for _, format := range []string{"text", "xml", "json", "yaml"} {
			run(fixture.Select("SELECT id FROM ae_role_insert_"+format+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT id,v FROM ae_role_merge_"+format+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(15)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
		}
	})
}

func autoExplainDMLFormatRoot(t *testing.T, body, format string) map[string]any {
	t.Helper()
	var document map[string]any
	if format == "xml" {
		var envelope overexplainXML
		if err := xml.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatal(err)
		}
		var findPlan func(overexplainXML) map[string]any
		findPlan = func(element overexplainXML) map[string]any {
			if element.XMLName.Local == "Plan" {
				return overexplainXMLValue(t, element, "").(map[string]any)
			}
			for _, child := range element.Children {
				if plan := findPlan(child); plan != nil {
					return plan
				}
			}
			return nil
		}
		root := findPlan(envelope)
		if root == nil {
			t.Fatal("XML DML Plan absent", body)
		}
		explainJSONVisit(root, func(node map[string]any) {
			for _, key := range []string{"Actual Rows", "Actual Loops"} {
				value, ok := node[key].(string)
				if !ok {
					t.Fatal("XML DML actual absent", node)
				}
				number, err := strconv.ParseFloat(value, 64)
				if err != nil {
					t.Fatal(err)
				}
				node[key] = number
			}
		})
		return root
	}
	if format == "yaml" {
		if err := yaml.Unmarshal([]byte(body), &document); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		body = string(encoded)
	}
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	root, ok := document["Plan"].(map[string]any)
	if !ok {
		t.Fatal("structured DML Plan absent", document)
	}
	return root
}
