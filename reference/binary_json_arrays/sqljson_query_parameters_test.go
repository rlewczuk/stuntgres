package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryParameters(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Run("same document path bindings", func(t *testing.T) {
			q := `SELECT JSON_EXISTS($1::jsonb,$2::jsonpath PASSING $3::int AS v) AS present,JSON_QUERY($1::jsonb,$2::jsonpath PASSING $3::int AS v) AS item,JSON_VALUE($1::jsonb,$2::jsonpath PASSING $3::int AS v RETURNING int DEFAULT -1 ON EMPTY DEFAULT -2 ON ERROR) AS value`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			ok := func(doc, path, variable any, row []any) {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"present", "item", "value"}, []string{"BOOL", "JSONB", "INT4"}, [][]any{row}, doc, path, variable))
			}
			ok(sqljsonQueryDocument, `$.s`, 2, []any{true, `"123"`, int64(123)})
			ok(`{}`, `$.missing`, 2, []any{false, nil, int64(-1)})
			ok(sqljsonQueryDocument, `$.a[*]`, 2, []any{true, nil, int64(-2)})
			ok(`{}`, `$v`, 7, []any{true, `7`, int64(7)})
			ok(`{}`, `$v`, nil, []any{true, `null`, nil})
			for _, tc := range []struct{ doc, path, code, message, detail, context string }{
				{`x`, `$`, "22P02", `invalid input syntax for type json`, `Token "x" is invalid.`, `JSON data, line 1: x`},
				{`{}`, `$[`, "42601", `syntax error at end of jsonpath input`, "", ""},
			} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				s := fixture.ErrorDiagnostic(q, d, tc.doc, tc.path, 2)
				s.SelectRows, s.ErrorStage = true, "execute"
				pg := d
				if tc.code == "22P02" {
					pg.Context = "JSON data, line 1: x\nunnamed portal parameter $1 = '...'"
				} else {
					pg.Context = "unnamed portal parameter $2 = '...'"
				}
				s.PostgresDiagnostic = &pg
				s.Diagnostic = &pg
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				ok(sqljsonQueryDocument, `$.s`, 2, []any{true, `"123"`, int64(123)})
			}
			ok(nil, `$`, 2, []any{nil, nil, nil})
			ok(`{}`, nil, 2, []any{nil, nil, nil})
			ok(sqljsonQueryDocument, `$.s`, 2, []any{true, `"123"`, int64(123)})
		})
		t.Run("same held current environment", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`SET DateStyle TO 'ISO, YMD'`, 0))
			run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
			q := `SELECT JSON_VALUE('{}','$v' PASSING $1::timestamptz AS v) AS sql,JSON_QUERY('{}','$v' PASSING $1::timestamptz AS v) AS json,JSON_VALUE('{}','$v' PASSING $1::timestamptz AS v RETURNING jsonb) AS value_json`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ zone, text string }{{"UTC", "2018-02-21 02:34:56+00"}, {"America/Los_Angeles", "2018-02-20 18:34:56-08"}, {"UTC", "2018-02-21 02:34:56+00"}} {
				run(fixture.Exec(`SET TIME ZONE '`+tc.zone+`'`, 0))
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"sql", "json", "value_json"}, []string{"TEXT", "JSONB", "JSONB"}, [][]any{{tc.text, `"2018-02-21T02:34:56+00:00"`, `"2018-02-21T02:34:56+00:00"`}}, `2018-02-21 12:34:56+10`))
			}
			dateSQL := `SELECT JSON_VALUE('{}','$v' PASSING $1::date AS v) AS sql,JSON_QUERY('{}','$v' PASSING $1::date AS v) AS json`
			dateStmt, err := c.PrepareContext(p.Ctx, dateSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer dateStmt.Close()
			dh := fixture.HeldStatement{Executor: c, Statement: dateStmt, SQL: dateSQL}
			for _, tc := range []struct{ style, text string }{{"ISO, YMD", "2018-02-21"}, {"SQL, DMY", "21/02/2018"}, {"ISO, YMD", "2018-02-21"}} {
				run(fixture.Exec(`SET DateStyle TO '`+tc.style+`'`, 0))
				fixture.RunStep(t, p.Ctx, dh, b.Recorder, fixture.Select(dateSQL, []string{"sql", "json"}, []string{"TEXT", "JSONB"}, [][]any{{tc.text, `"2018-02-21"`}}, `2018-02-21`))
			}
			compareSQL := `SELECT JSON_VALUE('{}','$a == $b' PASSING $1::timestamp AS a,$2::timestamptz AS b RETURNING boolean) AS v`
			compareStmt, err := c.PrepareContext(p.Ctx, compareSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer compareStmt.Close()
			ch := fixture.HeldStatement{Executor: c, Statement: compareStmt, SQL: compareSQL}
			for _, tc := range []struct {
				zone string
				want bool
			}{{"UTC", false}, {"America/Los_Angeles", true}, {"UTC", false}} {
				run(fixture.Exec(`SET TIME ZONE '`+tc.zone+`'`, 0))
				fixture.RunStep(t, p.Ctx, ch, b.Recorder, fixture.SQLJSONQueryOne(compareSQL, "BOOL", tc.want, `2018-02-20 18:34:56`, `2018-02-21 02:34:56+00`))
				// Current local date plus supplied clock, not the transaction's
				// current instant, owns the time-to-timetz offset.
				s := fixture.SQLJSONQueryOne(`SELECT JSON_VALUE('{}','$a == $b' PASSING TIME '12:00' AS a,((CURRENT_DATE + TIME '12:00') AT TIME ZONE '`+tc.zone+`')::timetz AS b RETURNING boolean) AS v`, "BOOL", true)
				fixture.SQLJSONBoth(t, p, b, c, s)
			}
		})
	})
}
