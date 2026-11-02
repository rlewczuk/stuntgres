package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainOptions(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainSeed(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			cases := []struct {
				option  string
				enabled bool
			}{
				{"DEBUG", true}, {"DeBuG", true}, {`"debug"`, true},
				{"DEBUG TRUE", true}, {"DEBUG ON", true}, {"DEBUG 1", true},
				{"DEBUG 'true'", true}, {"DEBUG 'on'", true}, {"DEBUG 'TrUe'", true},
				{"DEBUG FALSE", false}, {"DEBUG OFF", false}, {"DEBUG 0", false},
				{"DEBUG 'false'", false}, {"DEBUG 'off'", false}, {"DEBUG 'OfF'", false},
				{"DEBUG TRUE, DEBUG FALSE", false}, {"DEBUG FALSE, DEBUG TRUE", true},
				{"DEBUG, ANALYZE TRUE, ANALYZE FALSE", true},
			}
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/prepared=%t", tc.option, prepared), func(t *testing.T) {
						q := overexplainSQL("JSON", tc.option, "UPDATE overexplain_data SET v='changed' RETURNING id")
						r := overexplainQuery(t, p, c, q, "JSON", prepared)
						overexplainDebug(t, b, r, "JSON", tc.enabled)
					})
				}
			}
			for _, prepared := range []bool{false, true} {
				q := "EXPLAIN (DEBUG, FORMAT XML, FORMAT JSON) SELECT 1"
				overexplainDebug(t, b, overexplainQuery(t, p, c, q, "JSON", prepared), "JSON", true)
			}
			overexplainInvariant(t, p, b, c)
		})
	})
	cases := []struct{ name, query, code, message, token, stage string }{
		{"unknown", "EXPLAIN (DEBUFF) SELECT 1", "42601", `unrecognized EXPLAIN option "debuff"`, "DEBUFF", "execute"},
		{"quoted-name", `EXPLAIN ("DEBUG") SELECT 1`, "42601", `unrecognized EXPLAIN option "DEBUG"`, `"DEBUG"`, "execute"},
		{"null", "EXPLAIN (DEBUG NULL) SELECT 1", "42601", `syntax error at or near "NULL"`, "NULL", "prepare"},
		{"parameter", "EXPLAIN (DEBUG $1) SELECT 1", "42601", `syntax error at or near "$1"`, "$1", "prepare"},
		{"equals", "EXPLAIN (DEBUG = true) SELECT 1", "42601", `syntax error at or near "="`, "=", "prepare"},
		{"legacy", "EXPLAIN DEBUG SELECT 1", "42601", `syntax error at or near "DEBUG"`, "DEBUG", "prepare"},
		{"bad-format", "EXPLAIN (DEBUG, FORMAT bad) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "bad"`, "FORMAT", "execute"},
		{"earlier-format", "EXPLAIN (FORMAT bad, FORMAT JSON, DEBUG) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "bad"`, "FORMAT", "execute"},
		{"quoted-format", "EXPLAIN (DEBUG, FORMAT 'JSON') SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "JSON"`, "FORMAT", "execute"},
		{"missing-format", "EXPLAIN (DEBUG, FORMAT) SELECT 1", "42601", "format requires a parameter", "", "prepare"},
		{"child-relation", "EXPLAIN (DEBUG 'bad') SELECT * FROM overexplain_absent", "42P01", `relation "overexplain_absent" does not exist`, "overexplain_absent", "prepare"},
		{"child-column", "EXPLAIN (DEBUG 'bad') SELECT absent FROM overexplain_data", "42703", `column "absent" does not exist`, "absent", "prepare"},
	}
	for _, value := range []string{"'1'", "'0'", "2", "1.0", "'yes'", "'bad'", "'NULL'"} {
		cases = append(cases, struct{ name, query, code, message, token, stage string }{"boolean-" + value, "EXPLAIN (DEBUG " + value + ") SELECT 1", "42601", "debug requires a Boolean value", "", "execute"})
	}
	cases = append(cases, struct{ name, query, code, message, token, stage string }{"earlier-boolean", "EXPLAIN (DEBUG 'bad', DEBUG FALSE, ANALYZE) UPDATE overexplain_data SET id=99", "42601", "debug requires a Boolean value", "", "execute"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + tc.query
					overexplainError(t, p, b, c, q, overexplainDiagnostic(q, tc.code, tc.message, tc.token), tc.stage, true)
				}
				overexplainInvariant(t, p, b, c)
			})
		})
	}
	t.Run("held-before-load", func(t *testing.T) {
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var handles []*sql.Stmt
			defer func() {
				for _, stmt := range handles {
					stmt.Close()
				}
			}()
			for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
				q := overexplainSQL(format, "DEBUG", "SELECT 1")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("must defer DEBUG recognition until Execute", err)
				}
				handles = append(handles, stmt)
				s := fixture.ErrorDiagnostic(q, overexplainDiagnostic(q, "42601", `unrecognized EXPLAIN option "debug"`, "DEBUG"))
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			}
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			for i, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
				rows, err := handles[i].QueryContext(p.Ctx)
				if err != nil {
					t.Fatal("same held handle after LOAD", err)
				}
				overexplainDebug(t, b, overexplainRows(t, rows, format), format, true)
			}
		})
	})
	t.Run("transaction-and-whole-input", func(t *testing.T) {
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "LOAD 'pg_overexplain'; SELECT ("
			s := fixture.ErrorDiagnostic(q, overexplainDiagnostic(q, "42601", "syntax error at end of input", ""))
			s.Diagnostic.Position = fixture.Position(len([]rune(q)) + 1)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			overexplainUnloaded(t, p, b, c)
			overexplainSeed(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			overexplainExec(t, p, b, c, "BEGIN")
			overexplainExec(t, p, b, c, "SAVEPOINT before_debug")
			q = "EXPLAIN (ANALYZE, DEBUG 'bad') UPDATE overexplain_data SET id=99"
			s = fixture.ErrorDiagnostic(q, overexplainDiagnostic(q, "42601", "debug requires a Boolean value", ""))
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted())
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			overexplainExec(t, p, b, c, "ROLLBACK TO before_debug")
			overexplainDebug(t, b, overexplainQuery(t, p, c, "EXPLAIN (DEBUG) SELECT 1", "TEXT", true), "TEXT", true)
			overexplainExec(t, p, b, c, "ROLLBACK")
			overexplainInvariant(t, p, b, c)
		})
	})
}
