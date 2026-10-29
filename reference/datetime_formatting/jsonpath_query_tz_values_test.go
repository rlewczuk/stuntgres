package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const jsonpathQueryTZDocument = `"2015-08-01 12:00:00-05"`
const jsonpathQueryTZVars = `{"limit":"2015-08-02"}`
const jsonpathQueryTZPath = `$ ? (@.datetime() < $limit.datetime())`
const jsonpathQueryTZBoolean = `$.datetime() < $limit.datetime()`

var jsonpathQueryTZFunctions = []struct {
	name, typ string
	oid       int
	set       bool
}{
	{"jsonb_path_exists_tz", "BOOL", 1177, false},
	{"jsonb_path_match_tz", "BOOL", 2030, false},
	{"jsonb_path_query_tz", "JSONB", 1179, true},
	{"jsonb_path_query_array_tz", "JSONB", 1180, false},
	{"jsonb_path_query_first_tz", "JSONB", 2023, false},
}

func jsonpathQueryTZExpected(name string, yes bool) [][]any {
	switch name {
	case "jsonb_path_exists_tz", "jsonb_path_match_tz":
		return jsonpathQueryRows(yes)
	case "jsonb_path_query_array_tz":
		if yes {
			return jsonpathQueryRows(`["2015-08-01 12:00:00-05"]`)
		}
		return jsonpathQueryRows(`[]`)
	case "jsonb_path_query_tz":
		if yes {
			return jsonpathQueryRows(jsonpathQueryTZDocument)
		}
		return nil
	default:
		if yes {
			return jsonpathQueryRows(jsonpathQueryTZDocument)
		}
		return jsonpathQueryRows(nil)
	}
}

func jsonpathQueryTZArgs(name string) []any {
	path := jsonpathQueryTZPath
	if name == "jsonb_path_match_tz" {
		path = jsonpathQueryTZBoolean
	}
	return []any{jsonpathQueryTZDocument, path, jsonpathQueryTZVars, false}
}

func TestReferenceJSONPathQueryTZValues(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, zone := range []string{"UTC", "Asia/Tokyo"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE '`+zone+`'`, 0))
			for _, f := range jsonpathQueryTZFunctions {
				q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, zone == "UTC"), jsonpathQueryTZArgs(f.name)...))
			}
		}
	})
}

func TestReferenceJSONPathQueryTZTimezoneBoundary(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		for _, f := range jsonpathQueryTZFunctions {
			for _, silent := range []bool{false, true} {
				args := jsonpathQueryTZArgs(f.name)
				args[3] = silent
				q := `SELECT ` + strings.TrimSuffix(f.name, "_tz") + `($1,$2,$3,$4) AS changed`
				d := fixture.ControlsReferenceDiagnostic("0A000", "cannot convert value from date to timestamptz without time zone usage")
				d.Hint = "Use *_tz() function for time zone support."
				s := fixture.ErrorDiagnostic(q, d, args...)
				s.SelectRows = true
				fixture.JSONPathNavigateBoth(t, p, b, c, s)
			}
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match('["2023-08-15","2023-08-15 00:00:00"]','$[0].date() == $[1].timestamp()') AS changed`, "BOOL", true))
	})
}

func TestReferenceJSONPathQueryTZConversions(t *testing.T) {
	for _, zone := range []string{"UTC", "Asia/Tokyo", "Etc/GMT+1"} {
		t.Run(zone, func(t *testing.T) {
			jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE '`+zone+`'`, 0))
				suffix, date, clock, stamp := "+00:00", "2023-08-15", "00:30:00", "2023-08-15T00:30:00"
				if zone == "Asia/Tokyo" {
					suffix, date, clock, stamp = "+09:00", "2023-08-15", "09:30:00", "2023-08-15T09:30:00"
				}
				if zone == "Etc/GMT+1" {
					suffix, date, clock, stamp = "-01:00", "2023-08-14", "23:30:00", "2023-08-14T23:30:00"
				}
				for _, tc := range []struct{ doc, path, want string }{
					{`["2023-08-15","2023-08-15 12:34:56"]`, `$[*].timestamp_tz()`, `["2023-08-15T00:00:00` + suffix + `", "2023-08-15T12:34:56` + suffix + `"]`},
					{`"2023-08-15 00:30:00+00"`, `$.date()`, `["` + date + `"]`},
					{`"2023-08-15 00:30:00+00"`, `$.time()`, `["` + clock + `"]`},
					{`"2023-08-15 00:30:00+00"`, `$.timestamp()`, `["` + stamp + `"]`},
					{`"12:34:56"`, `$.time_tz()`, `["12:34:56` + suffix + `"]`},
					{`"2023-08-15 12:34:56+05:30"`, `$.datetime()`, `["2023-08-15T12:34:56+05:30"]`},
					{`"2023-08-15 12:34:56+05:30"`, `$.timestamp_tz()`, `["2023-08-15T12:34:56+05:30"]`},
					{`["2023-08-15","00:00:00"]`, `$[0].date() == $[1].time()`, `[null]`},
				} {
					fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_query_array_tz($1,$2) AS changed`, "JSONB", tc.want, tc.doc, tc.path))
				}
				for _, method := range []string{"date", "timestamp"} {
					doc := `["2023-08-15 00:00:00","2023-08-15 00:00:00` + suffix + `"]`
					if method == "date" {
						doc = `["2023-08-15","2023-08-15 00:00:00` + suffix + `"]`
					}
					for _, path := range []string{`$[0].` + method + `() == $[1].timestamp_tz()`, `$[1].timestamp_tz() == $[0].` + method + `() `} {
						fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match_tz($1,$2) AS changed`, "BOOL", true, doc, path))
					}
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match_tz($1,'$[0].time() == $[1].time_tz()') AS changed`, "BOOL", true, `["12:00:00","12:00:00`+suffix+`"]`))
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT jsonb_path_query_tz('["2023-08-15","2023-08-16"]','$[*].timestamp_tz()') AS changed ORDER BY changed`, "JSONB", jsonpathQueryRows(`"2023-08-15T00:00:00`+suffix+`"`, `"2023-08-16T00:00:00`+suffix+`"`)))
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_query_first_tz('"2023-08-15"','$.timestamp_tz()') AS changed`, "JSONB", `"2023-08-15T00:00:00`+suffix+`"`))
			})
		})
	}
}
