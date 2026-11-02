package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeFormats(t *testing.T) {
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		t.Run(format, func(t *testing.T) {
			overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainRangeSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				for _, child := range []string{"SELECT 1", "SELECT NULL::text", `SELECT "q & <"."001" FROM range_src AS "q & <"("001", "v & <")`, "SELECT id FROM range_src WHERE id=99"} {
					for _, prepared := range []bool{false, true} {
						for _, options := range []string{"RANGE_TABLE, COSTS FALSE, VERBOSE FALSE", "RANGE_TABLE, VERBOSE, COSTS TRUE", "RANGE_TABLE, DEBUG, VERBOSE, COSTS FALSE", "RANGE_TABLE FALSE, COSTS FALSE", "COSTS FALSE"} {
							t.Run(fmt.Sprintf("%s/%s/prepared=%t", child, options, prepared), func(t *testing.T) {
								r := overexplainQuery(t, p, c, overexplainSQL(format, options, child), format, prepared)
								enabled := options != "RANGE_TABLE FALSE, COSTS FALSE" && options != "COSTS FALSE"
								entries := overexplainRange(t, b, r, format, enabled)
								if enabled && format != "TEXT" && (child == "SELECT 1" || child == "SELECT NULL::text") {
									for _, e := range entries {
										if e["Kind"] == "relation" {
											t.Fatal("constant target invented relation", e)
										}
									}
									if !b.PG && len(entries) != 0 {
										t.Fatal("zero-range SELECT has entries", entries)
									}
								}
								if enabled && format != "TEXT" && child == "SELECT id FROM range_src WHERE id=99" && !b.PG {
									e := overexplainRangeNamed(t, b, entries, "relation", "range_src")
									overexplainRangeRelation(t, p, c, e, "range_src", format)
									if len(overexplainRangeList(t, e["Required Permissions"])) == 0 {
										t.Fatal("COSTS/VERBOSE hid required permissions", e)
									}
								}
							})
						}
					}
				}
				q := overexplainSQL(format, "RANGE_TABLE, VERBOSE, COSTS FALSE", "SELECT $1::integer AS n FROM range_src WHERE id=$1")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var retained overexplainResult
				var snapshot string
				for i, arg := range []any{int64(1), int64(2), nil} {
					rows, err := stmt.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					r := overexplainRows(t, rows, format)
					overexplainRange(t, b, r, format, true)
					// Direct and fresh-prepared parameter routes must also accept NULL.
					overexplainRange(t, b, overexplainQuery(t, p, c, q, format, false, arg), format, true)
					overexplainRange(t, b, overexplainQuery(t, p, c, q, format, true, arg), format, true)
					if i == 0 {
						retained, snapshot = r, fmt.Sprint(r)
					}
					if fmt.Sprint(retained) != snapshot {
						t.Fatal("held parameter execution mutated previous document")
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT $1::integer AS n", []string{"n"}, []string{"INT4"}, [][]any{{arg}}, arg)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
				// Exec owns and drains a real EXPLAIN result without executing its child.
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(overexplainSQL(format, "RANGE_TABLE", "UPDATE range_src SET id=99"), 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				overexplainRangeValues(t, p, b, c)
				q = overexplainSQL(format, "RANGE_TABLE, ANALYZE, COSTS FALSE, TIMING FALSE", "SELECT id FROM range_src")
				r := overexplainQuery(t, p, c, q, format, true)
				overexplainRange(t, b, r, format, true)
				overexplainRangeActualRows(t, r, format, 3)
				for _, prepared := range []bool{false, true} {
					overexplainExec(t, p, b, c, "BEGIN")
					q = overexplainSQL(format, "RANGE_TABLE, ANALYZE, COSTS FALSE, TIMING FALSE", "UPDATE range_src SET v='executed' WHERE id=1 RETURNING id")
					overexplainRange(t, b, overexplainQuery(t, p, c, q, format, prepared), format, true)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM range_src WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"executed"}}))
					overexplainExec(t, p, b, c, "ROLLBACK")
					overexplainRangeValues(t, p, b, c)
				}
			})
		})
	}
}

func overexplainRangeActualRows(t *testing.T, r overexplainResult, format string, want int64) {
	t.Helper()
	if format == "TEXT" {
		explainContains(t, []string{r.text}, fmt.Sprintf("rows=%d", want), "loops=1")
		return
	}
	root := overexplainRangeMap(t, r.document[0]["Plan"])
	if overexplainNumber(t, root["Actual Rows"], format == "XML") != float64(want) || overexplainNumber(t, root["Actual Loops"], format == "XML") != 1 {
		t.Fatal("RANGE_TABLE changed ANALYZE execution measurements", root)
	}
}
