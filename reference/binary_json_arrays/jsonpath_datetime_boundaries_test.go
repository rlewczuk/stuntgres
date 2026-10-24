package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeBoundaries(t *testing.T) {
	for _, tc := range []struct{ input, method string }{
		{"1900-02-29", "datetime"},
		{"12:60:00", "time"}, {"12:34:61", "time"}, {"24:00:00.000001", "time"},
		{"12:34:56+16:00", "time_tz"}, {"12:34:56+05:60", "time_tz"},
		{"2023-08-15 12:34:56+16:00", "timestamp_tz"},
		{"2023-08-15 12:34:56 trailing", "timestamp"},
	} {
		t.Run(tc.input+tc.method, func(t *testing.T) {
			jsonpathDatetimeDiagnostic(t, jsonpathDatetimeString(tc.input), `$.`+tc.method+`()`, jsonpathDatetimeFormatError(tc.method, tc.input))
		})
	}
	for _, tc := range []struct{ input, path, want string }{
		{"2000-02-29", `$.date()`, "2000-02-29"},
		// Zero month/day fields are accepted by the formatting reader. Its
		// missing-field defaults predict January and the first day, respectively.
		{"2023-00-15", `$.date()`, "2023-01-15"},
		{"2023-08-00", `$.date()`, "2023-08-01"},
		{"12:34:56.000001", `$.time(6)`, "12:34:56.000001"},
		{"23:59:59.999999+05:30", `$.time_tz(0)`, "24:00:00+05:30"},
		{"2023-12-31 23:59:59.999999+05:30", `$.timestamp_tz(0)`, "2024-01-01T00:00:00+05:30"},
	} {
		t.Run(tc.input+tc.path, func(t *testing.T) {
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), tc.path, jsonpathDatetimeArray(tc.want))
		})
	}
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Temporal items survive bindings/current-item filters and explicit typed
		// SQLJSON PASSING without being reclassified as ordinary JSON strings.
		q := `SELECT JSON_QUERY('"2023-08-15"','$.date() ? (@ == $d)' PASSING DATE '2023-08-15' AS d ERROR ON ERROR) AS changed`
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `"2023-08-15"`))
		for _, tc := range []struct{ path, want string }{
			{`$[*] ? (@.date() == $d.date()).date()`, `["2023-08-15"]`},
			{`$d.date()`, `["2023-08-15"]`},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathDatetimeSQL, tc.want, `["2023-08-15","2023-08-16"]`, tc.path, `{"d":"2023-08-15"}`, false))
		}
		// Suppression inside a predicate differs from a hard timezone error.
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-15"]`, `["2023-08-15","bad"]`, `$[*] ? (@.date() == "2023-08-15".date())`, `{}`, false))
	})
}
