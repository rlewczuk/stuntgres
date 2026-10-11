package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Prerequisite-owned input messages and parameter Context are independently
// specified by the existing date/timestamp/interval reference contracts.
// This scenario exercises their integration through age/isfinite, not new input rules.
func TestReferenceDatetimeAgeFiniteChildDiagnostics(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ name, query, code, message string }{
			{"age_malformed", "SELECT age(TIMESTAMP 'nonsense')", "22007", `invalid input syntax for type timestamp: "nonsense"`},
			{"age_range", "SELECT age(TIMESTAMP '294277-01-01',TIMESTAMP '2000-01-01')", "22008", `timestamp out of range: "294277-01-01"`},
			{"date_malformed", "SELECT isfinite(DATE 'bad')", "22007", `invalid input syntax for type date: "bad"`},
			{"date_range", "SELECT isfinite(DATE '5874898-01-01')", "22008", `date out of range: "5874898-01-01"`},
			{"timestamp_malformed", "SELECT isfinite(TIMESTAMP 'nonsense')", "22007", `invalid input syntax for type timestamp: "nonsense"`},
			{"timestamp_range", "SELECT isfinite(TIMESTAMP '294277-01-01')", "22008", `timestamp out of range: "294277-01-01"`},
			{"interval_malformed", "SELECT isfinite(INTERVAL 'invalid')", "22007", `invalid input syntax for type interval: "invalid"`},
			{"interval_range", "SELECT isfinite(INTERVAL '2147483648 days')", "22015", `interval field value out of range: "2147483648 days"`},
			{"null_does_not_hide_literal", "SELECT age(NULL::timestamp,TIMESTAMP 'nonsense')", "22007", `invalid input syntax for type timestamp: "nonsense"`},
			{"literal_before_null", "SELECT age(TIMESTAMP 'nonsense',NULL::timestamp)", "22007", `invalid input syntax for type timestamp: "nonsense"`},
			{"input_before_wrong_arity", "SELECT age(TIMESTAMP 'nonsense',TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01')", "22007", `invalid input syntax for type timestamp: "nonsense"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// The failing literal's quote has an independently character-counted
				// source position; no engine diagnostic participates in this expectation.
				position := len([]rune(tc.query[:strings.Index(tc.query, "'")])) + 1
				datetimeAgeFiniteFailure(t, p, b, c, tc.query, tc.code, tc.message, "", position)
				datetimeAgeFiniteValue(t, p, b, c, "isfinite(TIMESTAMP '2000-01-01')", "BOOL", true)
			})
		}
		for _, q := range []string{
			"SELECT age(NULL::timestamp,TIMESTAMP '2000-01-01'+INTERVAL '1 hour'/0)",
			"SELECT age(TIMESTAMP '2000-01-01'+INTERVAL '1 hour'/0,NULL::timestamp)",
		} {
			t.Run(q, func(t *testing.T) {
				// Input analysis succeeds; constant-child execution/preflight fails
				// without a parse location, notwithstanding the strict typed NULL.
				datetimeAgeFiniteFailure(t, p, b, c, q, "22012", "division by zero", "", 0)
			})
		}
		for _, tc := range []struct {
			name, query, typ string
			failures         []struct {
				args          []any
				code, message string
				parameter     int
			}
			validArgs []any
			valid     any
		}{
			{"age", "SELECT age($1::timestamp,$2::timestamp) AS result", "INTERVAL", []struct {
				args          []any
				code, message string
				parameter     int
			}{
				{[]any{"nonsense", nil}, "22007", `invalid input syntax for type timestamp: "nonsense"`, 1},
				{[]any{nil, "nonsense"}, "22007", `invalid input syntax for type timestamp: "nonsense"`, 2},
				{[]any{nil, "294277-01-01"}, "22008", `timestamp out of range: "294277-01-01"`, 2},
			}, []any{"2004-06-01", "2004-04-30"}, "1 mon 1 day"},
			{"date", "SELECT isfinite($1::date) AS result", "BOOL", []struct {
				args          []any
				code, message string
				parameter     int
			}{
				{[]any{"bad"}, "22007", `invalid input syntax for type date: "bad"`, 1},
				{[]any{"5874898-01-01"}, "22008", `date out of range: "5874898-01-01"`, 1},
			}, []any{"2000-01-01"}, true},
			{"timestamp", "SELECT isfinite($1::timestamp) AS result", "BOOL", []struct {
				args          []any
				code, message string
				parameter     int
			}{
				{[]any{"nonsense"}, "22007", `invalid input syntax for type timestamp: "nonsense"`, 1},
				{[]any{"294277-01-01"}, "22008", `timestamp out of range: "294277-01-01"`, 1},
			}, []any{"2000-01-01"}, true},
			{"interval", "SELECT isfinite($1::interval) AS result", "BOOL", []struct {
				args          []any
				code, message string
				parameter     int
			}{
				{[]any{"invalid"}, "22007", `invalid input syntax for type interval: "invalid"`, 1},
				{[]any{"2147483648 days"}, "22015", `interval field value out of range: "2147483648 days"`, 1},
			}, []any{"1 mon -2 days"}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, held := range []bool{false, true} {
					t.Run(fmt.Sprintf("held=%t", held), func(t *testing.T) {
						x := fixture.Executor(c)
						if held {
							statement, err := c.PrepareContext(p.Ctx, tc.query)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
							x = fixture.HeldStatement{Executor: c, Statement: statement, SQL: tc.query}
						}
						for _, failure := range tc.failures {
							d := fixture.ControlsReferenceDiagnostic(failure.code, failure.message)
							d.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", failure.parameter)
							s := fixture.ErrorDiagnostic(tc.query, d, failure.args...)
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
							// SAME held statement succeeds after every failed import. Result
							// metadata/raw codec and values are not derived from the failure.
							success := fixture.Select(tc.query, []string{"result"}, []string{tc.typ}, [][]any{{tc.valid}}, tc.validArgs...)
							success.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
								meta, err := rows.ColumnTypes()
								if err != nil {
									t.Fatal(err)
								}
								datetimeArithmeticMetadata(t, b.PG, meta)
							}
							fixture.RunStep(t, p.Ctx, x, b.Recorder, success)
							args := make([]any, len(tc.validArgs))
							success.Args, success.Rows = args, [][]any{{nil}}
							fixture.RunStep(t, p.Ctx, x, b.Recorder, success)
						}
					})
				}
			})
		}
		q := "SELECT age($1::timestamp,TIMESTAMP '2000-01-01'+INTERVAL '1 hour'/$2::integer) AS result"
		statement, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		x := fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("runtime_sibling_held=%t", held), func(t *testing.T) {
				executor := fixture.Executor(c)
				if held {
					executor = x
				}
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), nil, 0)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"result"}, []string{"INTERVAL"}, [][]any{{nil}}, nil, 1))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"result"}, []string{"INTERVAL"}, [][]any{{"01:00:00"}}, "2000-01-01 02:00", 1))
			})
		}
	})
}
