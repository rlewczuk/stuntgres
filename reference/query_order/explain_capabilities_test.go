package reference_test

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func mustExplainCapabilityRows(t *testing.T, p *fixture.Pair, tx explainAnalyzeDB, q string, prepared bool) *sql.Rows {
	t.Helper()
	if prepared {
		stmt, err := tx.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stmt.Close() })
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows, err := tx.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestReferenceExplainCapabilities(t *testing.T) {
	t.Run("utility-children", explainCapabilityUtilities)
	cases := []struct {
		name, prefix, message, token, typ string
		analyze                           bool
	}{
		{"xml", "EXPLAIN (FORMAT XML)", "EXPLAIN format XML is not supported", "FORMAT", "XML", false},
		{"yaml", "EXPLAIN (FORMAT YAML)", "EXPLAIN format YAML is not supported", "FORMAT", "TEXT", false},
		{"settings", "EXPLAIN (SETTINGS)", "EXPLAIN option SETTINGS is not supported", "SETTINGS", "TEXT", false},
		{"buffers", "EXPLAIN (BUFFERS)", "EXPLAIN option BUFFERS is not supported", "BUFFERS", "TEXT", false},
		{"memory", "EXPLAIN (MEMORY)", "EXPLAIN option MEMORY is not supported", "MEMORY", "TEXT", false},
		{"generic", "EXPLAIN (GENERIC_PLAN)", "EXPLAIN option GENERIC_PLAN is not supported", "GENERIC_PLAN", "TEXT", false},
		{"wal", "EXPLAIN (ANALYZE, WAL)", "EXPLAIN option WAL is not supported", "WAL", "TEXT", true},
		{"json-buffers", "EXPLAIN (FORMAT JSON, BUFFERS)", "EXPLAIN option BUFFERS is not supported", "BUFFERS", "JSON", false},
		{"json-wal", "EXPLAIN (FORMAT JSON, ANALYZE, WAL)", "EXPLAIN option WAL is not supported", "WAL", "JSON", true},
		{"serialize-text", "EXPLAIN (ANALYZE, SERIALIZE TEXT)", "EXPLAIN option SERIALIZE is not supported", "SERIALIZE", "TEXT", true},
		{"serialize-binary", "EXPLAIN (ANALYZE, SERIALIZE BINARY)", "EXPLAIN option SERIALIZE is not supported", "SERIALIZE", "TEXT", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE explain_cap(id integer)", 0))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO explain_cap VALUES(1)", 1))
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
							// Roll back even real ANALYZE DML; native rejection must not
							// reach any mutation. No nontransactional sequence is used.
							tx, err := b.DB.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							q := "/*é*/ " + c.prefix + " UPDATE explain_cap SET id=2 RETURNING id"
							if !b.PG {
								want := fixture.ControlsReferenceDiagnostic("0A000", c.message)
								want.Position = fixture.Position(len([]rune(q[:strings.Index(q, c.token)])) + 1)
								s := fixture.ErrorDiagnostic(q, want)
								s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
								if prepared && (c.name == "xml" || c.name == "yaml") {
									s.ErrorStage = "prepare"
								}
								fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
							} else {
								var text string
								consume := func() {
									var rows = mustExplainCapabilityRows(t, p, tx, q, prepared)
									defer rows.Close()
									cols, err := rows.Columns()
									if err != nil || len(cols) != 1 || cols[0] != "QUERY PLAN" {
										t.Fatalf("columns: %v, %v", cols, err)
									}
									types, err := rows.ColumnTypes()
									if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != c.typ {
										t.Fatalf("want %s column: %v, %v", c.typ, types, err)
									}
									for rows.Next() {
										var line string
										if err := rows.Scan(&line); err != nil {
											t.Fatal(err)
										}
										text += line + "\n"
									}
									if err := rows.Err(); err != nil {
										t.Fatal(err)
									}
								}
								consume()
								if strings.TrimSpace(text) == "" {
									t.Fatal("empty capability plan")
								}
								switch c.name {
								case "json-buffers", "json-wal":
									var plans []struct{ Plan map[string]any }
									if err := json.Unmarshal([]byte(text), &plans); err != nil || len(plans) != 1 || plans[0].Plan["Node Type"] == nil {
										t.Fatalf("invalid JSON plan: %s, %v", text, err)
									}
								case "xml":
									d := xml.NewDecoder(strings.NewReader(text))
									found := false
									for {
										tok, err := d.Token()
										if err == io.EOF {
											break
										}
										if err != nil {
											t.Fatal(err)
										}
										if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "Plan" {
											found = true
										}
									}
									if !found {
										t.Fatalf("XML lacks Plan: %s", text)
									}
								case "yaml":
									explainContains(t, []string{text}, "Plan:", "Node Type:", "explain_cap")
								default:
									explainContains(t, []string{text}, "explain_cap", "rows=")
								}
								wantID := int64(1)
								if c.analyze {
									wantID = 2
									if c.typ == "JSON" {
										explainContains(t, []string{text}, `"Actual Rows"`, `"Execution Time"`)
									} else {
										explainContains(t, []string{text}, "actual time=", "Execution Time:")
									}
								}
								fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM explain_cap", []string{"id"}, []string{"INT4"}, [][]any{{wantID}}))
							}
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM explain_cap", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						})
					}
				})
			}
		})
	}
}

func explainCapabilityUtilities(t *testing.T) {
	cases := []struct{ name, child, token string }{
		{"execute", "EXECUTE explain_utility_stmt", "EXECUTE"},
		{"declare", "DECLARE explain_utility_cursor CURSOR FOR SELECT id FROM explain_utility", "DECLARE"},
		{"create-as", "CREATE TABLE explain_utility_created AS SELECT id FROM explain_utility", "CREATE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					for _, q := range []string{
						"CREATE TABLE explain_utility(id integer)",
						"PREPARE explain_utility_stmt AS SELECT id FROM explain_utility",
					} {
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
					}
					defer conn.ExecContext(p.Ctx, "DEALLOCATE explain_utility_stmt")
					for _, prepared := range []bool{false, true} {
						q := "/*é*/ EXPLAIN " + c.child
						if !b.PG {
							want := fixture.ControlsReferenceDiagnostic("0A000", "EXPLAIN for this statement is not supported")
							want.Position = fixture.Position(len([]rune(q[:strings.Index(q, c.token)])) + 1)
							s := fixture.ErrorDiagnostic(q, want)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						} else {
							if prepared {
								stmt, err := conn.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								rows, err := stmt.QueryContext(p.Ctx)
								if err != nil {
									stmt.Close()
									t.Fatal(err)
								}
								explainContains(t, explainTextRows(t, rows), "explain_utility")
								stmt.Close()
							} else {
								rows, err := conn.QueryContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								explainContains(t, explainTextRows(t, rows), "explain_utility")
							}
						}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id FROM explain_utility", []string{"id"}, []string{"INT4"}, nil))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='explain_utility_created'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
}
