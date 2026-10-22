package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonTablePolicyBoundaries(t *testing.T) {
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, path := range []string{"$missing", "$.x"} {
			t.Run(path, func(t *testing.T) {
				q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v int PATH '` + path + `' NULL ON ERROR) EMPTY ON ERROR)`
				if path == "$.x" {
					q = `SELECT * FROM JSON_TABLE($1::jsonb,'$missing' COLUMNS(v int PATH '$') EMPTY ON ERROR)`
				}
				sqljsonDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42704", `could not find jsonpath variable "missing"`), false, `{}`)
			})
		}
		t.Run("unused ordinary provider error", func(t *testing.T) {
			sqljsonDiagnostic(t, p, b, c, `SELECT * FROM JSON_TABLE($1::jsonb,'$' PASSING (1/$2::int) AS unused COLUMNS(v int PATH '$'))`, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false, nil, 0)
		})
		t.Run("exists error", func(t *testing.T) {
			sqljsonDiagnostic(t, p, b, c, `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v bool EXISTS PATH 'strict $.x' ERROR ON ERROR))`, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`), false, `{}`)
		})
		for _, typ := range []string{"bigint", "real"} {
			t.Run(typ, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42804", `could not coerce ON ERROR expression (FALSE) to the RETURNING type`)
				d.Detail = `invalid input syntax for type ` + typ + `: "false"`
				sqljsonDiagnostic(t, p, b, c, `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(v `+typ+` EXISTS PATH '$'))`, d, false, `{}`)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jt_policy_positive AS int CHECK(VALUE>0)`, 0))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('{"v":-1,"s":"abcd"}','$' COLUMNS(a jt_policy_positive PATH '$.v' DEFAULT 7 ON ERROR,b varchar(3) PATH '$.s' DEFAULT 'ok' ON ERROR,c int PATH '$.missing' NULL ON EMPTY,d int PATH 'strict $.missing' NULL ON ERROR,e jt_policy_positive EXISTS PATH '$.missing' UNKNOWN ON ERROR))`, []string{"a", "b", "c", "d", "e"}, []string{"INT4", "VARCHAR", "INT4", "INT4", "INT4"}, [][]any{{int64(7), `ok`, nil, nil, nil}}))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('["s"]','$' COLUMNS(a jsonb PATH '$[0]' WITH CONDITIONAL WRAPPER,b jsonb PATH '$[0]' WITH UNCONDITIONAL WRAPPER,c text FORMAT JSON PATH '$[0]' OMIT QUOTES,d jsonb PATH '$.missing' DEFAULT '[]' ON EMPTY,e jsonb PATH 'strict $.missing' DEFAULT '{}' ON ERROR))`, []string{"a", "b", "c", "d", "e"}, []string{"JSONB", "JSONB", "TEXT", "JSONB", "JSONB"}, [][]any{{`"s"`, `["s"]`, `s`, `[]`, `{}`}}))
	})
}
