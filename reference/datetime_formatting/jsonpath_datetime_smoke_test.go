package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeFeatureSmoke(t *testing.T) {
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, want string }{
			{`"2023-08-15"`, `$.date()`, `["2023-08-15"]`},
			{`"12:34:56.789"`, `$.time(2)`, `["12:34:56.79"]`},
			{`"12:34:56.789+05:30"`, `$.time_tz(2)`, `["12:34:56.79+05:30"]`},
			{`"2023-08-15 12:34:56.789"`, `$.timestamp(2)`, `["2023-08-15T12:34:56.79"]`},
			{`"2023-08-15 12:34:56.789+05:30"`, `$.timestamp_tz(2)`, `["2023-08-15T12:34:56.79+05:30"]`},
			{`["12:30","18:40"]`, `$[*].datetime("HH24:MI")`, `["12:30:00", "18:40:00"]`},
			{`"2023-08-15"`, `$.date().type()`, `["date"]`},
			{`"2023-08-15 12:34:56"`, `$.timestamp().string()`, `["2023-08-15T12:34:56"]`},
			{`["2023-08-15","2023-08-15 00:00:00"]`, `$[0].date() == $[1].timestamp()`, `[true]`},
			{`["2023-08-15 12:34:56+05:30","2023-08-15 07:04:56+00"]`, `$[0].timestamp_tz() == $[1].timestamp_tz()`, `[true]`},
			{`["2023-08-15 12:34:56+05:30","2023-08-15 07:04:56+00"]`, `$[*].timestamp_tz()`, `["2023-08-15T12:34:56+05:30", "2023-08-15T07:04:56+00:00"]`},
			{`["2023-08-15","00:00:00"]`, `$[0].date() == $[1].time()`, `[null]`},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(jsonpathDatetimeSQL, tc.want, tc.doc, tc.path, `{}`, false), b.PG))
		}
		for _, fn := range []string{"jsonb_path_query", "jsonb_path_query_first"} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(`SELECT `+fn+`('"2023-08-15"','$.date()') AS changed`, `"2023-08-15"`), b.PG))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT value,n FROM jsonb_path_query('["2023-08-15","2023-08-16"]','$[*].date()') WITH ORDINALITY AS q(value,n) ORDER BY n`, []string{"value", "n"}, []string{"JSONB", "INT8"}, [][]any{{`"2023-08-15"`, int64(1)}, {`"2023-08-16"`, int64(2)}}))
		d := jsonpathDatetimeZoneError("date", "timestamptz")
		s := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, d, `"2023-08-15"`, `$.timestamp_tz()`, `{}`, true)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
		stmt, err := c.PrepareContext(p.Ctx, jsonpathDatetimeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: jsonpathDatetimeSQL}
		bad := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, jsonpathDatetimeFormatError("date", "bad"), `["2023-08-15","bad","2023-08-16"]`, `$[*].date()`, `{}`, false)
		bad.SelectRows = true
		for _, s := range []fixture.Step{
			bad,
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-15"]`, `["2023-08-15","bad","2023-08-16"]`, `$[*].date()`, `{}`, true),
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-16T00:00:00"]`, `"2023-08-16"`, `$.timestamp()`, `{}`, false),
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `["18:40:00"]`, `"18:40"`, `$.datetime("HH24:MI")`, `{}`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, tc := range []struct{ zone, want string }{{`'UTC'`, `["07:04:56+00:00"]`}, {`'-10:00'`, `["17:04:56+10:00"]`}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE `+tc.zone, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, tc.want, `"2023-08-15 12:34:56+05:30"`, `$.time_tz()`, `{}`, false))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		jsonpathDatetimeControls(t, p, b, c)
	})
}
