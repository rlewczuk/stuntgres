package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeOptions(t *testing.T) {
	t.Run("accepted-and-independent", func(t *testing.T) {
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainRangeUnloaded(t, p, b, c)
			overexplainRangeSeed(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			cases := []struct {
				option  string
				enabled bool
			}{
				{"RANGE_TABLE", true}, {"RaNgE_TaBlE", true}, {`"range_table"`, true},
				{"RANGE_TABLE TRUE", true}, {"RANGE_TABLE ON", true}, {"RANGE_TABLE 1", true},
				{"RANGE_TABLE 'TrUe'", true}, {"RANGE_TABLE 'oN'", true},
				{"RANGE_TABLE FALSE", false}, {"RANGE_TABLE OFF", false}, {"RANGE_TABLE 0", false},
				{"RANGE_TABLE 'FaLsE'", false}, {"RANGE_TABLE 'OfF'", false},
				{"RANGE_TABLE TRUE, RANGE_TABLE FALSE", false}, {"RANGE_TABLE FALSE, RANGE_TABLE TRUE", true},
			}
			for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
				for _, tc := range cases {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/prepared=%t", format, tc.option, prepared), func(t *testing.T) {
							q := overexplainSQL(format, tc.option+", COSTS FALSE", "UPDATE range_src SET v='changed' RETURNING id")
							overexplainRange(t, b, overexplainQuery(t, p, c, q, format, prepared), format, tc.enabled)
						})
					}
				}
				for _, rangeOn := range []bool{false, true} {
					for _, debugOn := range []bool{false, true} {
						options := fmt.Sprintf("RANGE_TABLE %t, DEBUG %t", rangeOn, debugOn)
						r := overexplainQuery(t, p, c, overexplainSQL(format, options, "SELECT id FROM range_src"), format, true)
						overexplainRange(t, b, r, format, rangeOn)
						overexplainDebug(t, b, r, format, debugOn)
					}
				}
			}
			overexplainRangeValues(t, p, b, c)
		})
	})
	cases := []struct{ name, query, code, message, token, stage string }{
		{"quoted-name", `EXPLAIN ("RANGE_TABLE") SELECT 1`, "42601", `unrecognized EXPLAIN option "RANGE_TABLE"`, `"RANGE_TABLE"`, "execute"},
		{"unknown", "EXPLAIN (range_tables, ANALYZE) UPDATE range_src SET id=99", "42601", `unrecognized EXPLAIN option "range_tables"`, "range_tables", "execute"},
		{"null", "EXPLAIN (RANGE_TABLE NULL) SELECT 1", "42601", `syntax error at or near "NULL"`, "NULL", "prepare"},
		{"parameter", "EXPLAIN (RANGE_TABLE $1) SELECT 1", "42601", `syntax error at or near "$1"`, "$1", "prepare"},
		{"equals", "EXPLAIN (RANGE_TABLE = true) SELECT 1", "42601", `syntax error at or near "="`, "=", "prepare"},
		{"legacy", "EXPLAIN RANGE_TABLE SELECT 1", "42601", `syntax error at or near "RANGE_TABLE"`, "RANGE_TABLE", "prepare"},
		{"format", "EXPLAIN (RANGE_TABLE, FORMAT bad) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "bad"`, "FORMAT", "execute"},
		{"missing-format", "EXPLAIN (RANGE_TABLE, FORMAT) SELECT 1", "42601", "format requires a parameter", "", "prepare"},
		{"child-relation", "EXPLAIN (RANGE_TABLE 'bad') SELECT * FROM range_absent", "42P01", `relation "range_absent" does not exist`, "range_absent", "prepare"},
		{"child-column", "EXPLAIN (RANGE_TABLE 'bad') SELECT absent FROM range_src", "42703", `column "absent" does not exist`, "absent", "prepare"},
		{"earlier-invalid", "EXPLAIN (RANGE_TABLE 'bad', RANGE_TABLE FALSE, ANALYZE) UPDATE range_src SET id=99", "42601", "range_table requires a Boolean value", "", "execute"},
	}
	for _, value := range []string{"2", "1.0", "'yes'", "'bad'", "'1'", "'0'", "'NULL'"} {
		cases = append(cases, struct{ name, query, code, message, token, stage string }{"boolean-" + value, "EXPLAIN (RANGE_TABLE " + value + ") SELECT 1", "42601", "range_table requires a Boolean value", "", "execute"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainRangeSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + tc.query
					overexplainError(t, p, b, c, q, overexplainDiagnostic(q, tc.code, tc.message, tc.token), tc.stage, true)
				}
				overexplainRangeValues(t, p, b, c)
			})
		})
	}
	t.Run("held-before-load", func(t *testing.T) {
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var held []*sql.Stmt
			defer func() {
				for _, stmt := range held {
					stmt.Close()
				}
			}()
			formats := []string{"TEXT", "XML", "JSON", "YAML"}
			for _, format := range formats {
				q := overexplainSQL(format, "RANGE_TABLE", "SELECT 1")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("recognition must be deferred until Execute", err)
				}
				held = append(held, stmt)
				s := fixture.ErrorDiagnostic(q, overexplainDiagnostic(q, "42601", `unrecognized EXPLAIN option "range_table"`, "RANGE_TABLE"))
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			}
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			for i, stmt := range held {
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				overexplainRange(t, b, overexplainRows(t, rows, formats[i]), formats[i], true)
			}
		})
	})
	t.Run("whole-input-and-transaction", func(t *testing.T) {
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "LOAD 'pg_overexplain'; SELECT ("
			d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			d.Position = fixture.Position(len([]rune(q)) + 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
			overexplainRangeUnloaded(t, p, b, c)
			overexplainRangeSeed(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			q = "EXPLAIN (RANGE_TABLE, ANALYZE) UPDATE range_src SET id=99; SELECT ("
			d.Position = fixture.Position(len([]rune(q)) + 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
			overexplainExec(t, p, b, c, "BEGIN")
			overexplainExec(t, p, b, c, "SAVEPOINT before_range")
			q = "EXPLAIN (ANALYZE, RANGE_TABLE 'bad') UPDATE range_src SET id=99"
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42601", "range_table requires a Boolean value"))
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted())
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			overexplainExec(t, p, b, c, "ROLLBACK TO before_range")
			overexplainRange(t, b, overexplainQuery(t, p, c, "EXPLAIN (RANGE_TABLE, FORMAT JSON) SELECT 1", "JSON", true), "JSON", true)
			overexplainExec(t, p, b, c, "ROLLBACK")
			overexplainRangeValues(t, p, b, c)
		})
	})
}
