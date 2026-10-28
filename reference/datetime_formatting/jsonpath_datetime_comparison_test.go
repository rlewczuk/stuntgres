package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeComparisons(t *testing.T) {
	for _, tc := range []struct{ doc, path, want string }{
		{`["2023-08-15","2023-08-15 00:00:00"]`, `$[0].date() == $[1].timestamp()`, `[true]`},
		{`["2023-08-15 12:34:56+05:30","2023-08-15 07:04:56+00"]`, `$[0].timestamp_tz() == $[1].timestamp_tz()`, `[true]`},
		{`["2023-08-15 12:34:56+05:30","2023-08-15 07:04:57+00"]`, `$[0].timestamp_tz() == $[1].timestamp_tz()`, `[false]`},
		{`["2023-08-15","2023-08-16"]`, `$[0].date() < $[1].date()`, `[true]`},
		{`["12:00:00.000001","12:00:00.000002"]`, `$[0].time() < $[1].time()`, `[true]`},
		{`["2023-08-15","00:00:00"]`, `$[0].date() == $[1].time()`, `[null]`},
		{`"2023-08-15"`, `$.date() == $`, `[null]`},
		{`"2023-08-15"`, `($.date() == $) is unknown`, `[true]`},
		{`["12:00:00+00","13:00:00+01"]`, `$[0].time_tz() == $[1].time_tz()`, `[false]`},
		{`["2023-08-15","2023-08-16"]`, `$[*] ? (@.date() > $[0].date()).date()`, `["2023-08-16"]`},
	} {
		t.Run(tc.path, func(t *testing.T) { jsonpathDatetimeValue(t, tc.doc, tc.path, tc.want) })
	}
	jsonpathDatetimeValue(t, `["2023-08-15 12:34:56+05:30","2023-08-15 07:04:56+00"]`, `$[*].timestamp_tz()`, `["2023-08-15T12:34:56+05:30", "2023-08-15T07:04:56+00:00"]`)
}

func TestReferenceJSONPathDatetimeTimezoneBoundary(t *testing.T) {
	for _, tc := range []struct{ doc, path, source, target string }{
		{`"2023-08-15 12:34:56+05:30"`, `$.date()`, "timestamptz", "date"},
		{`"12:34:56+05:30"`, `$.time()`, "timetz", "time"},
		{`"2023-08-15 12:34:56+05:30"`, `$.timestamp()`, "timestamptz", "timestamp"},
		{`"2023-08-15"`, `$.timestamp_tz()`, "date", "timestamptz"},
		{`"2023-08-15 12:34:56"`, `$.timestamp_tz()`, "timestamp", "timestamptz"},
		{`"12:34:56"`, `$.time_tz()`, "time", "timetz"},
		{`["2023-08-15","2023-08-15 00:00:00+00"]`, `$[0].date() == $[1].timestamp_tz()`, "date", "timestamptz"},
		{`["12:00:00","12:00:00+00"]`, `$[0].time() == $[1].time_tz()`, "time", "timetz"},
	} {
		t.Run(tc.path+tc.doc, func(t *testing.T) {
			jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := jsonpathDatetimeZoneError(tc.source, tc.target)
				for _, silent := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, d, tc.doc, tc.path, `{}`, silent)
					s.SelectRows = true
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
				for _, op := range []string{"@?", "@@"} {
					q := `SELECT $1::jsonb ` + op + ` $2::jsonpath`
					s := fixture.ErrorDiagnostic(q, d, tc.doc, tc.path)
					s.SelectRows = true
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
				q := `SELECT jsonb_path_exists($1,$2,silent=>true)`
				s := fixture.ErrorDiagnostic(q, d, tc.doc, tc.path)
				s.SelectRows = true
				fixture.JSONPathNavigateBoth(t, p, b, c, s)
			})
		})
	}
}

func TestReferenceJSONPathDatetimeSessionZone(t *testing.T) {
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		defer c.ExecContext(p.Ctx, `SET TIME ZONE 'UTC'`)
		stmt, err := c.PrepareContext(p.Ctx, jsonpathDatetimeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: jsonpathDatetimeSQL}
		doc := `"2023-08-15 12:34:56.789+05:30"`
		old, err := stmt.QueryContext(p.Ctx, doc, `$.time_tz(2)`, `{}`, false)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// pq rows hold their physical owner until consumed. Retained eager native
		// rows alone cross the settings boundary; the shared SQL remains identical.
		if b.PG {
			fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{`["07:04:56.79+00:00"]`}}, false)
			old.Close()
		}
		for _, tc := range []struct{ setting, want string }{{`'-10:00'`, `["17:04:56.79+10:00"]`}, {`'UTC'`, `["07:04:56.79+00:00"]`}} {
			run(fixture.Exec(`SET TIME ZONE `+tc.setting, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, tc.want, doc, `$.time_tz(2)`, `{}`, false))
			for _, method := range []string{"datetime", "timestamp_tz"} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-15T12:34:56.789+05:30"]`, doc, `$.`+method+`()`, `{}`, false))
			}
		}
		if !b.PG {
			fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{"JSONB"}, [][]any{{`["07:04:56.79+00:00"]`}}, false)
		}
		run(fixture.Exec(`BEGIN`, 0))
		run(fixture.Exec(`SET LOCAL TIME ZONE '-10:00'`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["17:04:56.79+10:00"]`, doc, `$.time_tz(2)`, `{}`, false))
		run(fixture.Exec(`ROLLBACK`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["07:04:56.79+00:00"]`, doc, `$.time_tz(2)`, `{}`, false))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["07:04:56.79+00:00"]`, doc, `$.time_tz(2)`, `{}`, false))
	})
}
