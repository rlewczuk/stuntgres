package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLPreparedDiagnostics(t *testing.T) {
	cases := []struct {
		name, setup, query string
		diagnostic         fixture.Diagnostic
	}{
		{"duplicate", "PREPARE p(int) AS SELECT $1", "PREPARE p(int) AS SELECT $1", sqlPreparedDiagnostic("42P05", `prepared statement "p" already exists`)},
		{"unknown_execute", "", "EXECUTE p", sqlPreparedMissing("p")},
		{"unknown_lookup_first", "", "EXECUTE p(1/0)", sqlPreparedMissing("p")},
		{"unknown_deallocate", "", "DEALLOCATE PREPARE p", sqlPreparedMissing("p")},
		{"missing_count", "PREPARE p(int,text) AS SELECT $1", "EXECUTE p(1/0)", sqlPreparedDiagnostic("42601", `wrong number of parameters for prepared statement "p"`)},
		{"extra_count", "PREPARE p(int,text) AS SELECT $1", "EXECUTE p(TRUE,'x',1/0)", sqlPreparedDiagnostic("42601", `wrong number of parameters for prepared statement "p"`)},
		{"boolean_actual", "PREPARE p(int) AS SELECT $1", "EXECUTE p(TRUE)", sqlPreparedDiagnostic("42804", "parameter $1 of type boolean cannot be coerced to the expected type integer")},
		{"literal_actual", "PREPARE p(int) AS SELECT $1", "EXECUTE p('bad')", sqlPreparedDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)},
		{"duplicate_column_precedence", "PREPARE p(int) AS SELECT $1", "PREPARE p AS SELECT missing", sqlPreparedDiagnostic("42703", `column "missing" does not exist`)},
		{"duplicate_type_precedence", "PREPARE p(int) AS SELECT $1", "PREPARE p(no_such_type) AS SELECT 1", sqlPreparedDiagnostic("42704", `type "no_such_type" does not exist`)},
		{"unknown_unused", "", "PREPARE p(unknown) AS SELECT 1", sqlPreparedDiagnostic("42P18", "could not determine data type of parameter $1")},
		{"gap", "", "PREPARE p AS SELECT $2", sqlPreparedDiagnostic("42P18", "could not determine data type of parameter $1")},
		{"sparse_gap", "", "PREPARE p AS SELECT $10000", sqlPreparedDiagnostic("42P18", "could not determine data type of parameter $1")},
		{"occurrence_conflict", "CREATE TABLE prep_conflict(v int)", "PREPARE p AS SELECT $1 FROM prep_conflict WHERE v=$1", sqlPreparedDiagnostic("42P08", "inconsistent types deduced for parameter $1")},
		{"subquery", "PREPARE p(int) AS SELECT $1", "EXECUTE p((SELECT 1))", sqlPreparedDiagnostic("0A000", "cannot use subquery in EXECUTE parameter")},
		{"aggregate", "PREPARE p(int) AS SELECT $1", "EXECUTE p(count(*))", sqlPreparedDiagnostic("42803", "aggregate functions are not allowed in EXECUTE parameters")},
		{"window", "PREPARE p(int) AS SELECT $1", "EXECUTE p(row_number() OVER())", sqlPreparedDiagnostic("42P20", "window functions are not allowed in EXECUTE parameters")},
		{"no_namespace", "PREPARE p(int) AS SELECT $1", "EXECUTE p(v)", sqlPreparedDiagnostic("42703", `column "v" does not exist`)},
		{"utf8_position", "PREPARE p(int) AS SELECT $1", "/* é */ EXECUTE p(TRUE)", sqlPreparedDiagnostic("42804", "parameter $1 of type boolean cannot be coerced to the expected type integer")},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					if tc.setup != "" {
						sqlPreparedStep(t, p, b, c, fixture.Exec(tc.setup, 0))
					}
					d := tc.diagnostic
					switch tc.name {
					case "missing_count":
						d.Detail = "Expected 2 parameters but got 1."
					case "extra_count":
						d.Detail = "Expected 2 parameters but got 3."
					case "boolean_actual", "utf8_position":
						d.Hint = "You will need to rewrite or cast the expression."
						d.Position = sqlPreparedPosition(tc.query, "TRUE")
					case "literal_actual":
						d.Position = sqlPreparedPosition(tc.query, "'bad'")
					case "duplicate_column_precedence":
						d.Position = sqlPreparedPosition(tc.query, "missing")
					case "duplicate_type_precedence":
						d.Position = sqlPreparedPosition(tc.query, "no_such_type")
					case "occurrence_conflict":
						d.Detail = "integer versus text"
						d.Position = sqlPreparedPosition(tc.query, "$1")
					case "subquery":
						d.Position = sqlPreparedPosition(tc.query, "(SELECT")
					case "aggregate":
						d.Position = sqlPreparedPosition(tc.query, "count")
					case "window":
						d.Position = sqlPreparedPosition(tc.query, "row_number")
					case "no_namespace":
						d.Position = sqlPreparedPosition(tc.query, "v)")
					}
					s := sqlPreparedError(tc.query, d, prepared)
					sqlPreparedStep(t, p, b, c, s)
					if tc.name == "duplicate" || tc.name == "duplicate_column_precedence" || tc.name == "duplicate_type_precedence" {
						sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE p(7)", "?column?", "INT4", int64(7)))
					}
					if tc.setup == "" || tc.name == "occurrence_conflict" {
						sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
					}
				})
			})
		}
	}
}

func TestReferenceSQLPreparedSyntax(t *testing.T) {
	for _, tc := range []struct{ query, token string }{
		{"PREPARE p AS CREATE TABLE forbidden(v int)", "CREATE"},
		{"PREPARE p AS SET application_name TO forbidden", "SET"},
		{"PREPARE p AS SHOW application_name", "SHOW"},
		{"PREPARE p AS BEGIN", "BEGIN"},
		{"PREPARE p AS PREPARE nested AS SELECT 1", "PREPARE nested"},
		{"PREPARE p() AS SELECT 1", ")"},
		{"EXECUTE p()", ")"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.query, prepared), func(t *testing.T) {
				sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					token := tc.token
					if token == "PREPARE nested" {
						token = "PREPARE"
					}
					d := sqlPreparedDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", token))
					d.Position = sqlPreparedPosition(tc.query, tc.token)
					s := sqlPreparedError(tc.query, d, prepared)
					if prepared {
						s.ErrorStage = "prepare"
					}
					sqlPreparedStep(t, p, b, c, s)
					sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
				})
			})
		}
	}
}
