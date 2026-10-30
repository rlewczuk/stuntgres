package reference_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type explainJSONDocument []map[string]any

func explainJSONRows(t *testing.T, rows *sql.Rows) (explainJSONDocument, string) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"QUERY PLAN"}) {
		t.Fatalf("columns=%v: %v", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "JSON" {
		t.Fatalf("expected JSON descriptor: %v, %v", types, err)
	}
	if !rows.Next() {
		t.Fatalf("missing document: %v", rows.Err())
	}
	var text string
	if err := rows.Scan(&text); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("JSON plan returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var document explainJSONDocument
	if err := json.Unmarshal([]byte(text), &document); err != nil || len(document) == 0 {
		t.Fatalf("invalid document %q: %v", text, err)
	}
	for _, entry := range document {
		node, ok := entry["Plan"].(map[string]any)
		if !ok {
			t.Fatalf("missing Plan object: %v", entry)
		}
		explainJSONVisit(node, func(n map[string]any) {
			if typ, ok := n["Node Type"].(string); !ok || typ == "" {
				t.Fatalf("missing Node Type: %v", n)
			}
			if children, exists := n["Plans"]; exists {
				list, ok := children.([]any)
				if !ok || len(list) == 0 {
					t.Fatalf("invalid Plans: %v", n)
				}
				for _, child := range list {
					if _, ok := child.(map[string]any); !ok {
						t.Fatalf("invalid child: %v", child)
					}
				}
			}
			for _, key := range []string{"Plan Rows", "Logical Cost", "Startup Cost", "Total Cost", "Plan Width", "Actual Rows", "Actual Loops", "Actual Total Time"} {
				if value, exists := n[key]; exists {
					if number, ok := value.(float64); !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
						t.Fatalf("invalid numeric %s=%v", key, value)
					}
				}
			}
		})
	}
	return document, text
}

func explainJSONQuery(t *testing.T, p *fixture.Pair, executor fixture.Executor, query string, args ...any) (explainJSONDocument, string) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	return explainJSONRows(t, rows)
}

func explainJSONVisit(node map[string]any, visit func(map[string]any)) {
	visit(node)
	children, _ := node["Plans"].([]any)
	for _, child := range children {
		if object, ok := child.(map[string]any); ok {
			explainJSONVisit(object, visit)
		}
	}
}

func explainJSONNodes(document explainJSONDocument) []map[string]any {
	var nodes []map[string]any
	for _, entry := range document {
		explainJSONVisit(entry["Plan"].(map[string]any), func(node map[string]any) { nodes = append(nodes, node) })
	}
	return nodes
}

func explainJSONFind(t *testing.T, document explainJSONDocument, field string, value any) map[string]any {
	t.Helper()
	for _, node := range explainJSONNodes(document) {
		if reflect.DeepEqual(node[field], value) {
			return node
		}
	}
	t.Fatalf("missing %s=%v in %v", field, value, document)
	return nil
}

func explainJSONAbsent(t *testing.T, document explainJSONDocument, keys ...string) {
	t.Helper()
	for _, node := range explainJSONNodes(document) {
		for _, key := range keys {
			if _, exists := node[key]; exists {
				t.Errorf("unexpected %s in %v", key, node)
			}
		}
	}
}

func explainJSONOrdinary(t *testing.T, b fixture.Backend, document explainJSONDocument) {
	t.Helper()
	explainJSONAbsent(t, document, "Actual Rows", "Actual Loops", "Actual Startup Time", "Actual Total Time", "Workers", "Workers Planned", "Workers Launched", "Shared Hit Blocks", "WAL Records")
	for _, entry := range document {
		if _, exists := entry["Execution Time"]; exists {
			t.Fatal("ordinary plan has execution time", entry)
		}
	}
	if !b.PG {
		explainJSONAbsent(t, document, "Startup Cost", "Total Cost", "Plan Width", "Parallel Aware", "Async Capable")
	}
}

func explainJSONList(t *testing.T, node map[string]any, key string, count int) []any {
	t.Helper()
	values, ok := node[key].([]any)
	if !ok || len(values) != count {
		t.Fatalf("%s must have %d complete elements: %v", key, count, node)
	}
	return values
}

func explainJSONContains(t *testing.T, value any, fragments ...string) {
	t.Helper()
	text := fmt.Sprint(value)
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing %q in %s", fragment, text)
		}
	}
}
