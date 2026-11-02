package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func overexplainRoutes(t *testing.T, p *fixture.Pair, c fixture.Executor, q, format string, check func(overexplainResult)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		check(overexplainQuery(t, p, c, q, format, prepared))
	}
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer held.Close()
	for range 2 {
		rows, err := held.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(q, err)
		}
		check(overexplainRows(t, rows, format))
	}
}

func overexplainGates(t *testing.T, b fixture.Backend, r overexplainResult, format string, verbose, costs bool) {
	t.Helper()
	if format == "TEXT" {
		if strings.Contains(r.text, "Output:") != verbose {
			t.Fatalf("VERBOSE=%t: %s", verbose, r.text)
		}
		if strings.Contains(r.text, "rows=") != costs {
			t.Fatalf("COSTS=%t: %s", costs, r.text)
		}
		return
	}
	root := r.document[0]["Plan"].(map[string]any)
	if _, exists := root["Output"]; exists != verbose {
		t.Fatalf("VERBOSE=%t: %v", verbose, root)
	}
	for _, n := range explainJSONNodes(r.document) {
		for _, key := range []string{"Plan Rows", "Logical Cost", "Startup Cost", "Total Cost", "Plan Width"} {
			_, exists := n[key]
			if !costs && exists {
				t.Fatalf("COSTS OFF retained %s: %v", key, n)
			}
		}
		if costs {
			if _, exists := n["Plan Rows"]; !exists {
				t.Fatal("missing estimates", n)
			}
		}
	}
	if !b.PG {
		found := false
		for _, n := range explainJSONNodes(r.document) {
			if d, ok := n["Embedded Debug"].(map[string]any); ok {
				if _, exists := d["Target List"]; exists {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("native DEBUG alone must project genuine targets, independent of VERBOSE")
		}
	}
}

func TestReferenceOverexplainFormats(t *testing.T) {
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		t.Run(format, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainSeed(t, p, b, c)
				const child = "SELECT id,v FROM overexplain_data WHERE id>1 ORDER BY id DESC"
				// General typed formats must work before the module is loaded.
				overexplainRoutes(t, p, c, overexplainSQL(format, "COSTS FALSE", child), format, func(r overexplainResult) { overexplainDebug(t, b, r, format, false) })
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				for _, option := range []string{"", "DEBUG FALSE", "DEBUG"} {
					overexplainRoutes(t, p, c, overexplainSQL(format, option, child), format, func(r overexplainResult) { overexplainDebug(t, b, r, format, option == "DEBUG") })
				}
				for _, verbose := range []bool{false, true} {
					for _, costs := range []bool{false, true} {
						q := overexplainSQL(format, fmt.Sprintf("DEBUG, VERBOSE %t, COSTS %t", verbose, costs), child)
						overexplainRoutes(t, p, c, q, format, func(r overexplainResult) {
							overexplainDebug(t, b, r, format, true)
							overexplainGates(t, b, r, format, verbose, costs)
						})
					}
				}
				for _, child := range []string{"SELECT", "SELECT NULL::text AS t,NULL::integer AS i"} {
					overexplainRoutes(t, p, c, overexplainSQL(format, "DEBUG, VERBOSE, COSTS FALSE", child), format, func(r overexplainResult) {
						overexplainDebug(t, b, r, format, true)
						if !b.PG && format != "TEXT" && child == "SELECT" {
							found := false
							for _, node := range explainJSONNodes(r.document) {
								if d, ok := node["Embedded Debug"].(map[string]any); ok {
									if targets, exists := d["Target List"]; exists {
										list, ok := targets.([]any)
										if !ok || len(list) != 0 {
											t.Fatal("empty target list lost", d)
										}
										found = true
									}
								}
							}
							if !found {
								t.Fatal("missing actual zero-width target owner")
							}
						}
					})
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT NULL::text AS t,NULL::integer AS i", []string{"t", "i"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}}))
				if format != "TEXT" {
					const escaped = "SELECT 'a,b:c''d\"<&'::text AS x,E'line\\nend'::text AS y,'none'::text,'true'::text,'1'::text,'&'::text,'<'::text,''::text"
					overexplainRoutes(t, p, c, overexplainSQL(format, "DEBUG, VERBOSE, COSTS FALSE", escaped), format, func(r overexplainResult) {
						output := explainJSONList(t, r.document[0]["Plan"].(map[string]any), "Output", 8)
						for i, value := range output {
							if _, ok := value.(string); !ok {
								t.Fatalf("Output[%d] became %T", i, value)
							}
						}
						if !strings.Contains(output[0].(string), "a,b:c''d\"<&") {
							t.Fatal("escaped SQL string split/corrupted", output)
						}
						for i, literal := range []string{"none", "true", "1", "&", "<", ""} {
							if !strings.Contains(output[i+2].(string), "'"+literal+"'") {
								t.Fatal("string-looking scalar changed", output)
							}
						}
						if !strings.Contains(output[1].(string), "'line\nend'") {
							t.Fatal("newline string changed during structured encoding", output)
						}
						overexplainDebug(t, b, r, format, true)
					})
					overexplainRoutes(t, p, c, overexplainSQL(format, "DEBUG, VERBOSE, COSTS FALSE", "VALUES (1,'a,b'::text),(2,NULL::text)"), format, func(r overexplainResult) {
						overexplainDebug(t, b, r, format, true)
						if b.PG {
							return
						} // PG presents its Values Scan, not native literal rows.
						values := explainJSONFind(t, r.document, "Node Type", "Values")
						list := explainJSONList(t, values, "Values", 2)
						for _, row := range list {
							columns, ok := row.([]any)
							if !ok || len(columns) != 2 {
								t.Fatal("nested VALUES boundary lost", list)
							}
						}
						explainJSONContains(t, list[0], "1", "a,b")
						explainJSONContains(t, list[1], "2", "NULL")
					})
				}
				overexplainInvariant(t, p, b, c)
			})
		})
	}
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		t.Run("analyze-"+format, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				for _, child := range []string{"SELECT id FROM overexplain_data ORDER BY id", "UPDATE overexplain_data SET v='changed' WHERE id=2 RETURNING id"} {
					for _, prepared := range []bool{false, true} {
						overexplainExec(t, p, b, c, "BEGIN")
						q := overexplainSQL(format, "DEBUG, ANALYZE, TIMING FALSE, SUMMARY TRUE, COSTS FALSE", child)
						r := overexplainQuery(t, p, c, q, format, prepared)
						overexplainDebug(t, b, r, format, true)
						wantRows := int64(3)
						if strings.HasPrefix(child, "UPDATE") {
							wantRows = 1
						}
						if format == "TEXT" {
							explainContains(t, []string{r.text}, fmt.Sprintf("actual rows=%d", wantRows), "loops=1", "Planning Time:", "Execution Time:")
						} else {
							root := r.document[0]["Plan"].(map[string]any)
							if overexplainNumber(t, root["Actual Rows"], format == "XML") != float64(wantRows) || overexplainNumber(t, root["Actual Loops"], format == "XML") != 1 {
								t.Fatal("lost actual observations", root)
							}
							for _, key := range []string{"Planning Time", "Execution Time"} {
								if _, exists := r.document[0][key]; !exists {
									t.Fatal("missing summary", key, r.document)
								}
							}
						}
						if wantRows == 1 {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM overexplain_data WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{"changed"}}))
						}
						overexplainExec(t, p, b, c, "ROLLBACK")
					}
				}
				overexplainInvariant(t, p, b, c)
			})
		})
	}
}
