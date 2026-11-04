package reference_test

import (
	"database/sql"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// Keep backend names immediately below the public family so oracle discovery
// can select /postgres without temporarily filtering the shared scenario.
func autoExplainScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.DB, b.Recorder = db, recorder
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			defer func() {
				if t.Failed() {
					c.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			body(t, p, b, c, &notices)
			autoExplainQuiet(t, &notices)
		})
	}
}

func autoExplainQuiet(t *testing.T, notices *[]fixture.Diagnostic) {
	t.Helper()
	if len(*notices) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", *notices)
	}
}

func autoExplainConfigure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
	t.Helper()
	for _, q := range []string{
		"LOAD 'auto_explain'",
		"SET auto_explain.log_level = notice",
		"SET auto_explain.log_format = json",
		"SET auto_explain.log_analyze = on",
		"SET auto_explain.log_timing = off",
		"SET auto_explain.sample_rate = 1",
		"SET auto_explain.log_min_duration = 0",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	autoExplainQuiet(t, notices)
}

var autoExplainDuration = regexp.MustCompile(`^duration: ([0-9]+\.[0-9]{3}) ms  plan:\n`)

// Only the measured duration and physical/logical plan choice vary. The complete
// portable diagnostic, source query, parameters and actual counts remain exact.
func autoExplainJSON(t *testing.T, notices *[]fixture.Diagnostic, query string, parameters *string, analyzed bool, rootRows float64) map[string]any {
	t.Helper()
	if len(*notices) != 1 {
		t.Fatalf("want one completed plan for %q, got %#v", query, *notices)
	}
	d := (*notices)[0]
	*notices = nil
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: d.Message}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("plan diagnostic got %#v want %#v", d, want)
	}
	match := autoExplainDuration.FindStringSubmatch(d.Message)
	if match == nil {
		t.Fatalf("invalid duration/plan envelope: %q", d.Message)
	}
	elapsed, err := strconv.ParseFloat(match[1], 64)
	if err != nil || elapsed < 0 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) {
		t.Fatalf("invalid elapsed execution duration %q: %v", match[1], err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(d.Message[len(match[0]):]), &document); err != nil || document == nil {
		t.Fatalf("auto_explain JSON must be an object, not SQL EXPLAIN's array: %q: %v", d.Message, err)
	}
	if document["Query Text"] != query {
		t.Fatalf("Query Text=%#v want %q", document["Query Text"], query)
	}
	gotParameters, present := document["Query Parameters"]
	if parameters == nil {
		if present {
			t.Fatalf("unexpected Query Parameters: %#v", gotParameters)
		}
	} else if !present || gotParameters != *parameters {
		t.Fatalf("Query Parameters=%#v want %q", gotParameters, *parameters)
	}
	root, ok := document["Plan"].(map[string]any)
	if !ok {
		t.Fatalf("missing Plan object: %#v", document)
	}
	explainJSONVisit(root, func(node map[string]any) {
		if typ, ok := node["Node Type"].(string); !ok || typ == "" {
			t.Fatalf("missing Node Type: %#v", node)
		}
		if children, exists := node["Plans"]; exists {
			list, ok := children.([]any)
			if !ok || len(list) == 0 {
				t.Fatalf("invalid Plans list: %#v", node)
			}
			for _, child := range list {
				if _, ok := child.(map[string]any); !ok {
					t.Fatalf("non-object plan child: %#v", child)
				}
			}
		}
		for _, key := range []string{"Actual Startup Time", "Actual Total Time"} {
			if _, exists := node[key]; exists {
				t.Fatalf("log_timing=off exposed %s: %#v", key, node)
			}
		}
		for _, key := range []string{"Actual Rows", "Actual Loops"} {
			value, exists := node[key]
			if !analyzed {
				if exists {
					t.Fatalf("log_analyze=off exposed %s: %#v", key, node)
				}
				continue
			}
			number, ok := value.(float64)
			if !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
				t.Fatalf("invalid %s: %#v", key, node)
			}
		}
	})
	if analyzed && (root["Actual Rows"] != rootRows || root["Actual Loops"] != float64(1)) {
		t.Fatalf("root actual rows/loops=%v/%v want %v/1", root["Actual Rows"], root["Actual Loops"], rootRows)
	}
	return document
}
