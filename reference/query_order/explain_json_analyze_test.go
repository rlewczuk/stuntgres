package reference_test

import (
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func explainJSONActual(t *testing.T, b fixture.Backend, document explainJSONDocument, count float64, timing, summary bool) {
	t.Helper()
	root := document[0]["Plan"].(map[string]any)
	if root["Actual Rows"] != count || root["Actual Loops"] != float64(1) {
		t.Fatalf("actual root=%v; want rows=%v loops=1", root, count)
	}
	for _, entry := range document {
		for _, key := range []string{"Planning Time", "Execution Time"} {
			value, exists := entry[key]
			if summary {
				if number, ok := value.(float64); !exists || !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
					t.Fatalf("missing measured %s: %v", key, entry)
				}
			} else if exists {
				t.Fatalf("summary false retained %s: %v", key, entry)
			}
		}
	}
	if !timing {
		explainJSONAbsent(t, document, "Actual Total Time", "Actual Startup Time")
	} else if _, ok := root["Actual Total Time"].(float64); !ok {
		t.Fatal("timing root missing", root)
	}
	if !b.PG {
		explainJSONAbsent(t, document, "Actual Startup Time", "Workers", "Workers Planned", "Workers Launched", "Shared Hit Blocks", "WAL Records", "Startup Cost", "Total Cost", "Plan Width")
	}
}

func TestReferenceExplainJSONAnalyze(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			explainJSONSetup(t, p, b)
			for _, timing := range []bool{false, true} {
				for _, summary := range []bool{false, true} {
					for _, tc := range []struct {
						query string
						count float64
					}{
						{"VALUES(1),(2),(3)", 3},
						{"SELECT id FROM explain_json WHERE id>1 LIMIT 1", 1},
						{"SELECT id FROM explain_json LIMIT 0", 0},
					} {
						query := fmt.Sprintf("EXPLAIN (FORMAT JSON, ANALYZE, COSTS false, TIMING %t, SUMMARY %t) %s", timing, summary, tc.query)
						explainJSONRoutes(t, p, b, query, func(document explainJSONDocument, _ string) {
							explainJSONActual(t, b, document, tc.count, timing, summary)
							if tc.count == 0 {
								knownZero := false
								for _, node := range explainJSONNodes(document) {
									if node["Actual Loops"] == float64(0) {
										knownZero = true
										if node["Actual Rows"] != float64(0) {
											t.Fatal(node)
										}
										if !b.PG {
											if _, exists := node["Actual Total Time"]; exists {
												t.Fatal("never-run time", node)
											}
										}
									}
								}
								if !knownZero {
									t.Fatal("never-demanded descendants not represented", document)
								}
							}
						})
					}
				}
			}
			explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,ANALYZE) VALUES(1),(2),(3)", func(document explainJSONDocument, _ string) { explainJSONActual(t, b, document, 3, true, true) })
			explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,SUMMARY true) SELECT id FROM explain_json", func(document explainJSONDocument, _ string) {
				explainJSONOrdinary(t, b, document)
				if _, ok := document[0]["Planning Time"].(float64); !ok {
					t.Fatal(document)
				}
			})
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					command string
					rows    [][]any
					count   float64
				}{
					{"INSERT INTO explain_json VALUES(4,'four'),(5,'five') RETURNING id", [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}, {int64(4), "four"}, {int64(5), "five"}}, 2},
					{"UPDATE explain_json SET v='changed' WHERE id>1 RETURNING id", [][]any{{int64(1), "one"}, {int64(2), "changed"}, {int64(3), "changed"}}, 2},
					{"DELETE FROM explain_json WHERE id>1 RETURNING id", [][]any{{int64(1), "one"}}, 2},
				} {
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer tx.Rollback()
						query := "EXPLAIN (FORMAT JSON,ANALYZE,TIMING false,SUMMARY false,COSTS false) " + tc.command
						rows := mustExplainCapabilityRows(t, p, tx, query, prepared)
						document, _ := explainJSONRows(t, rows)
						explainJSONActual(t, b, document, tc.count, false, false)
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows))
					}()
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
				}
			}
		})
	}
}
