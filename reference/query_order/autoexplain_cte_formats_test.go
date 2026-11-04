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

func autoExplainCTEFormats(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_cte_formats(id integer,v text)", 0))
		run(fixture.Exec("INSERT INTO ae_cte_formats VALUES(1,'one'),(2,NULL),(3,'three')", 3))
		autoExplainConfigure(t, p, b, c, notices)
		run(fixture.Exec("SET auto_explain.log_buffers=on", 0))
		run(fixture.Exec("SET auto_explain.log_wal=on", 0))
		const query = "WITH x AS MATERIALIZED (SELECT id,v FROM ae_cte_formats WHERE id<$1) SELECT id,v FROM x UNION ALL SELECT id,v FROM x ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		autoExplainQuiet(t, notices)
		for _, format := range []string{"text", "xml", "json", "yaml"} {
			for _, analyzed := range []bool{true, false} {
				run(fixture.Exec("SET auto_explain.log_format="+format, 0))
				run(fixture.Exec(fmt.Sprintf("SET auto_explain.log_analyze=%t", analyzed), 0))
				// The same handle must see every current producer predicate. Empty
				// output still enters the producer once and examines all three rows.
				for _, tc := range []struct {
					arg  int64
					rows [][]any
				}{
					{3, [][]any{{int64(1), "one"}, {int64(1), "one"}, {int64(2), nil}, {int64(2), nil}}},
					{4, [][]any{{int64(1), "one"}, {int64(1), "one"}, {int64(2), nil}, {int64(2), nil}, {int64(3), "three"}, {int64(3), "three"}}},
					{1, nil},
					{2, [][]any{{int64(1), "one"}, {int64(1), "one"}}},
					{3, [][]any{{int64(1), "one"}, {int64(1), "one"}, {int64(2), nil}, {int64(2), nil}}},
				} {
					s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows, tc.arg)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil || len(columns) != 2 {
							t.Fatalf("set-operation id/v descriptors: %v: %v", columns, err)
						}
						// UNION result columns carry no stored-column nullability
						// provenance; both adapters expose unknown, not nullable.
						for _, column := range columns {
							if nullable, known := column.Nullable(); nullable || known {
								t.Fatalf("set-operation %s nullability=%t/%t want false/false", column.Name(), nullable, known)
							}
						}
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, s)
					body := autoExplainMessage(t, notices, "NOTICE")
					parameters := fmt.Sprintf("$1 = '%d'", tc.arg)
					consumerRows := float64(len(tc.rows))
					if format == "text" {
						autoExplainCTEText(t, b, body, query, parameters, analyzed, consumerRows)
						continue
					}
					document := autoExplainCTEDocument(t, b, body, format)
					if document["Query Text"] != query || document["Query Parameters"] != parameters {
						t.Fatal("CTE current source/parameters", document)
					}
					autoExplainCTEStructured(t, b, document, format, analyzed, consumerRows)
				}
			}
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		autoExplainQuiet(t, notices)
		run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
		run(fixture.Select("SELECT id,v FROM ae_cte_formats ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		if !b.PG {
			// Automatic logging does not enable DEBUG. Exercise the same shared
			// wrapper with DEBUG enabled, so its lack of graph identity is not
			// a vacuous consequence of disabling debug output on every node.
			run(fixture.Exec("LOAD 'pg_overexplain'", 0))
			result := overexplainQuery(t, p, c, "EXPLAIN (ANALYZE, DEBUG, COSTS OFF, TIMING OFF, SUMMARY OFF, FORMAT JSON) "+query, "JSON", true, int64(3))
			if len(result.document) != 2 {
				t.Fatal("one consumer and one genuine CTE producer", result.document)
			}
			wrapper := result.document[1]["Plan"].(map[string]any)
			child := autoExplainCTEChild(t, wrapper)
			for _, key := range []string{"Embedded Debug", "Embedded Command Debug"} {
				if _, present := wrapper[key]; present {
					t.Fatal("synthetic CTE wrapper invented graph identity", wrapper)
				}
			}
			if _, ok := child["Embedded Debug"].(map[string]any); !ok {
				t.Fatal("genuine producer lost its graph identity", child)
			}
			autoExplainCTECount(t, wrapper, "json", 2)
			autoExplainCTECount(t, child, "json", 2)
			overexplainDebug(t, b, result, "JSON", true)
		}
	})
}

// Decode only the log envelope; reuse the existing structural XML decoder.
// XML has lexical scalars, while JSON/YAML retain independently checked types.
func autoExplainCTEDocument(t *testing.T, b fixture.Backend, body, format string) map[string]any {
	t.Helper()
	if format == "xml" {
		var envelope overexplainXML
		if err := xml.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.XMLName.Local != "explain" || envelope.XMLName.Space != "http://www.postgresql.org/2009/explain" {
			t.Fatal("CTE XML log envelope", envelope)
		}
		if !b.PG {
			if len(envelope.Children) != 1 || envelope.Children[0].XMLName.Local != "Query" {
				t.Fatal("native CTE XML query envelope", envelope)
			}
			return overexplainXMLValue(t, envelope.Children[0], "explain").(map[string]any)
		}
		// PostgreSQL auto_explain emits document properties directly beneath
		// explain; ordinary SQL EXPLAIN uses a separate Query wrapper.
		return overexplainXMLValue(t, envelope, "").(map[string]any)
	}
	var document map[string]any
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
	if err := json.Unmarshal([]byte(body), &document); err != nil || document == nil {
		t.Fatalf("CTE log must be an object: %v: %s", err, body)
	}
	return document
}

func autoExplainCTENumber(t *testing.T, value any, format string) float64 {
	t.Helper()
	number, ok := value.(float64)
	if format == "xml" {
		text, lexical := value.(string)
		if !lexical {
			t.Fatal("XML measurement must be lexical", value)
		}
		var err error
		number, err = strconv.ParseFloat(text, 64)
		ok = err == nil
	}
	if !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		t.Fatal("missing/nonfinite/negative actual measurement", value)
	}
	return number
}

func autoExplainCTECount(t *testing.T, node map[string]any, format string, rows float64) {
	t.Helper()
	if autoExplainCTENumber(t, node["Actual Rows"], format) != rows || autoExplainCTENumber(t, node["Actual Loops"], format) != 1 {
		t.Fatalf("CTE actual rows/loops must be %v/1: %#v", rows, node)
	}
}

func autoExplainCTEChild(t *testing.T, wrapper map[string]any) map[string]any {
	t.Helper()
	children, ok := wrapper["Plans"].([]any)
	if wrapper["Node Type"] != "CTE" || wrapper["CTE Name"] != "x" || !ok || len(children) != 1 {
		t.Fatal("ordinary CTE wrapper/real producer presentation", wrapper)
	}
	child, ok := children[0].(map[string]any)
	if !ok {
		t.Fatal("ordinary CTE child must be a real plan", children)
	}
	return child
}

func autoExplainCTEStructured(t *testing.T, b fixture.Backend, document map[string]any, format string, analyzed bool, consumerRows float64) {
	t.Helper()
	root, ok := document["Plan"].(map[string]any)
	if !ok {
		t.Fatal("CTE consumer Plan absent", document)
	}
	producer := root
	if !b.PG {
		producers, ok := document["Rewritten Plans"].([]any)
		if !ok || len(producers) != 1 {
			t.Fatal("single materialized CTE producer absent", document)
		}
		producer, ok = producers[0].(map[string]any)
		if !ok {
			t.Fatal("invalid CTE producer", producers)
		}
		autoExplainCTEChild(t, producer)
		if _, present := producer["Embedded Debug"]; present {
			t.Fatal("synthetic wrapper invented debug identity", producer)
		}
	} else {
		// PostgreSQL's real producer is an InitPlan within its physical tree,
		// not the native synthetic wrapper or a native Rewritten Plans entry.
		producer = explainJSONFind(t, explainJSONDocument{document}, "Subplan Name", "CTE x")
	}
	plans := []map[string]any{root}
	if !b.PG {
		plans = append(plans, producer)
	}
	for _, plan := range plans {
		explainJSONVisit(plan, func(node map[string]any) {
			if typ, ok := node["Node Type"].(string); !ok || typ == "" {
				t.Fatal("missing CTE node type", node)
			}
			for key, value := range node {
				if strings.HasPrefix(key, "Actual ") {
					if !analyzed || key != "Actual Rows" && key != "Actual Loops" {
						t.Fatal("CTE analyze/timing gating", node)
					}
					autoExplainCTENumber(t, value, format)
				}
				if !b.PG && (strings.Contains(key, "Blocks") || strings.HasPrefix(key, "WAL ")) {
					t.Fatal("fictional physical CTE metric", node)
				}
			}
			if analyzed {
				autoExplainCTENumber(t, node["Actual Rows"], format)
				autoExplainCTENumber(t, node["Actual Loops"], format)
			}
		})
	}
	if analyzed {
		autoExplainCTECount(t, root, format, consumerRows)
		autoExplainCTECount(t, producer, format, consumerRows/2)
		if !b.PG {
			autoExplainCTECount(t, autoExplainCTEChild(t, producer), format, consumerRows/2)
			if autoExplainCTENumber(t, document["Logical Tuple Accesses"], format) != 3 || autoExplainCTENumber(t, document["Logical Tuple Writes"], format) != 0 {
				t.Fatal("CTE base source must be examined once, never on cached replay", document)
			}
			if model, ok := document["Metric Model"].(string); !ok || !strings.Contains(model, "no physical") {
				t.Fatal("CTE logical metric model absent", document)
			}
		}
	} else if !b.PG {
		for _, key := range []string{"Metric Model", "Logical Tuple Accesses", "Logical Tuple Writes", "Triggers"} {
			if _, present := document[key]; present {
				t.Fatal("CTE analyze-off metric gating", document)
			}
		}
	}
}

var autoExplainCTETextActual = regexp.MustCompile(`\(actual rows=([0-9]+(?:\.[0-9]+)?) loops=([0-9]+)\)`)

func autoExplainCTEText(t *testing.T, b fixture.Backend, body, query, parameters string, analyzed bool, consumerRows float64) {
	t.Helper()
	prefix := "Query Text: " + query + "\nQuery Parameters: " + parameters + "\n"
	if !strings.HasPrefix(body, prefix) {
		t.Fatal("CTE text current source/parameters", body)
	}
	lines := strings.Split(strings.TrimPrefix(body, prefix), "\n")
	if strings.Contains(body, "actual time=") || strings.Contains(body, "Embedded Debug:") {
		t.Fatal("CTE timing/debug disabled", body)
	}
	producer := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "CTE x") {
			if producer != -1 {
				t.Fatal("CTE producer displayed more than once", body)
			}
			producer = i
		}
	}
	if producer < 0 || producer+1 >= len(lines) {
		t.Fatal("CTE wrapper/real producer absent from text", body)
	}
	if !analyzed {
		markers := []string{"(actual ", "(never executed)"}
		if !b.PG {
			markers = append(markers, "Logical Tuple Accesses:", "Logical Tuple Writes:", "Metric Model:")
		}
		for _, marker := range markers {
			if strings.Contains(body, marker) {
				t.Fatal("CTE text analyze-off gating", marker, body)
			}
		}
		return
	}
	count := func(line string, rows float64) {
		t.Helper()
		match := autoExplainCTETextActual.FindStringSubmatch(line)
		if len(match) != 3 {
			t.Fatal("CTE text actual count absent", line)
		}
		gotRows, err := strconv.ParseFloat(match[1], 64)
		if err != nil || gotRows != rows || match[2] != "1" {
			t.Fatalf("CTE text actual must be %v/1: %s", rows, line)
		}
	}
	count(lines[0], consumerRows)
	count(lines[producer+1], consumerRows/2)
	if !b.PG {
		count(lines[producer], consumerRows/2)
		for _, marker := range []string{"Logical Tuple Accesses: 3\n", "Logical Tuple Writes: 0", "no physical cache, page or WAL measurements"} {
			if !strings.Contains(body, marker) {
				t.Fatal("CTE once-only logical source metrics", marker, body)
			}
		}
	}
}
