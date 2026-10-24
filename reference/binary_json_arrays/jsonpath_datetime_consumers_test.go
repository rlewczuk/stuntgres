package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeSQLJSON(t *testing.T) {
	for _, tc := range []struct {
		expr, typ string
		want      any
	}{
		{`JSON_EXISTS('"2023-08-15"','$.date()' ERROR ON ERROR)`, "BOOL", true},
		{`JSON_QUERY('"2023-08-15"','$.date()' ERROR ON ERROR)`, "JSONB", `"2023-08-15"`},
		{`JSON_QUERY('["2023-08-15","2023-08-16"]','$[*].date()' WITH UNCONDITIONAL WRAPPER ERROR ON ERROR)`, "JSONB", `["2023-08-15", "2023-08-16"]`},
		{`JSON_VALUE('"2023-08-15"','$.date()' RETURNING date ERROR ON ERROR)::text`, "TEXT", "2023-08-15"},
		{`JSON_VALUE('"12:34:56.789"','$.time(2)' RETURNING time ERROR ON ERROR)::text`, "TEXT", "12:34:56.79"},
		{`JSON_VALUE('"2023-08-15 12:34:56"','$.timestamp()' RETURNING timestamp ERROR ON ERROR)::text`, "TEXT", "2023-08-15 12:34:56"},
		{`JSON_QUERY('{}','$v' PASSING TIMESTAMPTZ '2023-08-15 12:34:56+05:30' AS v ERROR ON ERROR)`, "JSONB", `"2023-08-15T07:04:56+00:00"`},
		{`JSON_VALUE('["2023-08-15","2023-08-15 00:00:00+00"]','$[0].date() == $[1].timestamp_tz()' RETURNING boolean ERROR ON ERROR)`, "BOOL", true},
		{`JSON_VALUE('{}','$a == $b' PASSING DATE '2023-08-15' AS a,TIMESTAMPTZ '2023-08-15 00:00:00+00' AS b RETURNING boolean ERROR ON ERROR)`, "BOOL", true},
		{`JSON_QUERY('"2023-08-15"','$.timestamp_tz()' ERROR ON ERROR)`, "JSONB", `"2023-08-15T00:00:00+00:00"`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
				jsonpathNavigateBoth(t, p, b, c, fixture.Select(`SELECT `+tc.expr+` AS changed`, []string{"changed"}, []string{tc.typ}, [][]any{{tc.want}}))
			})
		})
	}
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT j.n,j.d::text AS d,j.v FROM JSON_TABLE('["2023-08-15","2023-08-16"]','$[*]' COLUMNS(n FOR ORDINALITY,d date PATH '$.date()',v jsonb FORMAT JSON PATH '$.date()')) AS j ORDER BY j.n`
		jsonpathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"n", "d", "v"}, []string{"INT4", "TEXT", "JSONB"}, [][]any{{int64(1), "2023-08-15", `"2023-08-15"`}, {int64(2), "2023-08-16", `"2023-08-16"`}}))
		for _, tc := range jsonpathDatetimeISO {
			typ := map[string]string{"date": "DATE", "time": "TIME", "time_tz": "TIMETZ", "timestamp": "TIMESTAMP", "timestamp_tz": "TIMESTAMPTZ"}[tc.method]
			q := `SELECT JSON_VALUE(` + jsonReferenceQuote(jsonpathDatetimeString(tc.input)) + `,` + jsonReferenceQuote(`$.`+tc.method+`()`) + ` RETURNING ` + typ + ` ERROR ON ERROR) AS changed WHERE false`
			jsonpathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed"}, []string{typ}, nil))
		}
		// Method-producing SQLJSON paths have their own volatility contract;
		// even this date-only generated expression is rejected by PostgreSQL.
		// Successful persistent recipes use the ordinary fixed-zone consumers
		// exercised by Stored and Controls, not an immutability bypass.
		for _, tc := range []struct{ name, query string }{
			{"date-generated", `CREATE TABLE jpdt_immutable(doc jsonb,d date GENERATED ALWAYS AS (JSON_VALUE(doc,'$.date()' RETURNING date ERROR ON ERROR)) STORED)`},
			{"timestamptz-generated", `CREATE TABLE jpdt_mutable(doc jsonb,d timestamptz GENERATED ALWAYS AS (JSON_VALUE(doc,'$.timestamp_tz()' RETURNING timestamptz ERROR ON ERROR)) STORED)`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P17", `generation expression is not immutable`)
				jsonpathNavigateBoth(t, p, b, c, fixture.ErrorDiagnostic(tc.query, d))
			})
		}
	})
}
