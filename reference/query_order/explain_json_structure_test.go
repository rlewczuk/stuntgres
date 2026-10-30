package reference_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func explainJSONRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, check func(explainJSONDocument, string), args ...any) {
	t.Helper()
	document, text := explainJSONQuery(t, p, b.DB, query, args...)
	check(document, text)
	fresh, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fresh.QueryContext(p.Ctx, args...)
	if err != nil {
		fresh.Close()
		t.Fatal(err)
	}
	document, text = explainJSONRows(t, rows)
	fresh.Close()
	check(document, text)
	held, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for range 2 {
		rows, err := held.QueryContext(p.Ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		document, text = explainJSONRows(t, rows)
		check(document, text)
	}
}

func explainJSONSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE explain_json(id integer,v text)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO explain_json VALUES(1,'one'),(2,'two'),(3,'three')", 3))
}

func explainJSONScan(t *testing.T, b fixture.Backend, document explainJSONDocument, verbose, costs bool) {
	t.Helper()
	explainJSONOrdinary(t, b, document)
	scan := explainJSONFind(t, document, "Relation Name", "explain_json")
	if scan["Alias"] != "explain_json" {
		t.Fatal(scan)
	}
	if verbose && scan["Schema"] != "public" {
		t.Fatal("missing verbose schema", scan)
	}
	if !verbose {
		if _, exists := scan["Schema"]; exists {
			t.Fatal("nonverbose schema", scan)
		}
	}
	for _, node := range explainJSONNodes(document) {
		if costs {
			for _, key := range []string{"Plan Rows"} {
				if _, ok := node[key].(float64); !ok {
					t.Fatalf("missing numeric %s: %v", key, node)
				}
			}
			if !b.PG {
				if _, ok := node["Logical Cost"].(float64); !ok {
					t.Fatal(node)
				}
			}
			if b.PG {
				for _, key := range []string{"Startup Cost", "Total Cost", "Plan Width"} {
					if _, ok := node[key].(float64); !ok {
						t.Fatalf("missing PostgreSQL estimate %s: %v", key, node)
					}
				}
			}
		} else {
			for _, key := range []string{"Plan Rows", "Logical Cost", "Startup Cost", "Total Cost", "Plan Width", "Estimate Model"} {
				if _, exists := node[key]; exists {
					t.Fatalf("costs off retained %s: %v", key, node)
				}
			}
		}
	}
	if verbose {
		explainJSONList(t, document[0]["Plan"].(map[string]any), "Output", 1)
	} else {
		explainJSONAbsent(t, document, "Output")
	}
	if b.PG {
		explainJSONContains(t, document, "Sort Key", "DESC", "Filter", "id", ">")
		return
	}
	var types []string
	for _, node := range explainJSONNodes(document) {
		types = append(types, node["Node Type"].(string))
	}
	if !reflect.DeepEqual(types, []string{"Project", "Sort", "Filter", "Logical Scan"}) {
		t.Fatal("selected logical tree", types)
	}
	filter := explainJSONFind(t, document, "Node Type", "Filter")
	explainJSONContains(t, filter["Filter"], "id", ">", "1")
	sort := explainJSONFind(t, document, "Node Type", "Sort")
	keys := explainJSONList(t, sort, "Sort Key", 1)
	if !strings.Contains(keys[0].(string), "DESC") {
		t.Fatal(keys)
	}
}

func TestReferenceExplainJSONStructure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			explainJSONSetup(t, p, b)
			const query = "EXPLAIN (FORMAT JSON,COSTS false) SELECT id FROM explain_json WHERE id>1 ORDER BY id DESC"
			var first string
			explainJSONRoutes(t, p, b, query, func(document explainJSONDocument, text string) {
				explainJSONScan(t, b, document, false, false)
				if !b.PG && first != "" && text != first {
					t.Fatal("same-state JSON is nondeterministic")
				}
				if first == "" {
					first = text
				}
			})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(query, 0))
			const heldQuery = "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) SELECT id FROM explain_json WHERE id>$1 ORDER BY id DESC"
			held, err := b.DB.PrepareContext(p.Ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: heldQuery}
			old, oldText := explainJSONQuery(t, p, executor, heldQuery, int64(1))
			saved := fmt.Sprint(old)
			for _, sql := range []string{"ALTER TABLE explain_json RENAME TO explain_json_old", "CREATE TABLE explain_json(id integer,v text)", "INSERT INTO explain_json VALUES(4,'four')", "DROP TABLE explain_json_old"} {
				_, err := b.DB.ExecContext(p.Ctx, sql)
				if err != nil {
					t.Fatal(err)
				}
			}
			current, _ := explainJSONQuery(t, p, executor, heldQuery, int64(2))
			explainJSONFind(t, current, "Relation Name", "explain_json")
			if fmt.Sprint(old) != saved || !strings.Contains(oldText, "explain_json") {
				t.Fatal("retained document mutated across rebind")
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "four"}}))
		})
	}
}

func TestReferenceExplainJSONOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			explainJSONSetup(t, p, b)
			for _, verbose := range []bool{false, true} {
				for _, costs := range []bool{false, true} {
					query := fmt.Sprintf("EXPLAIN (FORMAT JSON,VERBOSE %t,COSTS %t) SELECT id FROM explain_json WHERE id>1 ORDER BY id DESC", verbose, costs)
					explainJSONRoutes(t, p, b, query, func(document explainJSONDocument, _ string) { explainJSONScan(t, b, document, verbose, costs) })
				}
			}
			for _, options := range []string{"FORMAT 'json', COSTS 0", "FORMAT JSON, COSTS 1", "FORMAT TEXT, FORMAT JSON, COSTS 'OFF'", "FORMAT JSON, VERBOSE 'TrUe', COSTS false", "FORMAT JSON, BUFFERS false, WAL false"} {
				explainJSONRoutes(t, p, b, "EXPLAIN ("+options+") SELECT 1", func(document explainJSONDocument, _ string) { explainJSONOrdinary(t, b, document) })
			}
			for _, prepared := range []bool{false, true} {
				explainQuery(t, p, b, "EXPLAIN (FORMAT JSON,FORMAT TEXT,COSTS false) SELECT 1", prepared)
			}
			const escaped = "EXPLAIN (FORMAT JSON, VERBOSE, COSTS false) SELECT 'a,b:c''d'::text AS x, E'line\\nend'::text AS y"
			explainJSONRoutes(t, p, b, escaped, func(document explainJSONDocument, _ string) {
				root := document[0]["Plan"].(map[string]any)
				values := explainJSONList(t, root, "Output", 2)
				if !strings.Contains(values[0].(string), "a,b:c''d") {
					t.Fatal("split or corrupted literal", values)
				}
			})
			explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) VALUES (1,'a,b:c'),(2,'d''e')", func(document explainJSONDocument, _ string) {
				if !b.PG {
					values := explainJSONFind(t, document, "Node Type", "Values")
					rows := explainJSONList(t, values, "Values", 2)
					if len(rows[0].([]any)) != 2 || len(rows[1].([]any)) != 2 {
						t.Fatal(rows)
					}
					explainJSONContains(t, rows[0], "1", "a,b:c")
					explainJSONContains(t, rows[1], "2", "d''e")
				}
			})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		})
	}
}
