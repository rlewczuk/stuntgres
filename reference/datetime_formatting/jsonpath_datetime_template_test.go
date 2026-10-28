package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeTemplates(t *testing.T) {
	for _, tc := range []struct{ input, template, want, label string }{
		{"15.08.2023", "DD.MM.YYYY", "2023-08-15", "date"},
		{"12:30", "HH24:MI", "12:30:00", "time without time zone"},
		{"2023-227", "YYYY-DDD", "2023-08-15", "date"},
		{"2023-33-2", "IYYY-IW-ID", "2023-08-15", "date"},
		{"15 August 2023", "DD Month YYYY", "2023-08-15", "date"},
		{"15 Aug 2023", "DD Mon YYYY", "2023-08-15", "date"},
		{"2023/08/15 12:34:56.123456 +05:30", "YYYY/MM/DD HH24:MI:SS.US TZH:TZM", "2023-08-15T12:34:56.123456+05:30", "timestamp with time zone"},
		{"12:34:56.123", "HH24:MI:SS.FF3", "12:34:56.123", "time without time zone"},
		{"12:34:56 +05:30", "HH24:MI:SS OF", "12:34:56+05:30", "time with time zone"},
		{"12:34:56 UTC", "HH24:MI:SS TZ", "12:34:56+00:00", "time with time zone"},
		{"2023年08月15日", `YYYY"年"MM"月"DD"日"`, "2023-08-15", "date"},
		{"2023X08X15", `YYYY"X"MM"X"DD`, "2023-08-15", "date"},
		{"2023'08'15", "YYYY'MM'DD", "2023-08-15", "date"},
	} {
		t.Run(tc.template, func(t *testing.T) {
			path := `$.datetime(` + jsonpathDatetimeString(tc.template) + `)`
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), path, jsonpathDatetimeArray(tc.want))
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), path+`.type()`, jsonpathDatetimeArray(tc.label))
		})
	}
	jsonpathDatetimeValue(t, `["12:30","18:40"]`, `$[*].datetime("HH24:MI")`, `["12:30:00", "18:40:00"]`)
}

func TestReferenceJSONPathDatetimeTemplateDiagnostics(t *testing.T) {
	for _, tc := range []struct{ input, template, code, message string }{
		{"2023/08/15", "YYYY-MM-DD", "22007", `unmatched format separator "-"`},
		{"2023Y08X15", `YYYY"X"MM"X"DD`, "22007", `unmatched format character "X"`},
		{"2023-08", "YYYY-MM-DD", "22007", `input string is too short for datetime format`},
		{"2023-08-15x", "YYYY-MM-DD", "22007", `trailing characters remain in input string after datetime format`},
		{"2023_08_15", "YYYY_MM_DD", "22007", `invalid datetime format separator: "_"`},
		{"2023-02-29", "YYYY-MM-DD", "22008", `date/time field value out of range: "2023-02-29"`},
		{"2023 2024", "YYYY YYYY", "22007", `conflicting values for "YYYY" field in formatting string`},
		{"2023 33", "YYYY IW", "22007", `invalid combination of date conventions`},
		{"x", `"x"`, "22007", `datetime format is not dated and not timed`},
	} {
		t.Run(tc.template+"/"+tc.input, func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			switch tc.template {
			case "YYYY YYYY":
				d.Detail = "This value contradicts a previous setting for the same field type."
			case "YYYY IW":
				d.Hint = "Do not mix Gregorian and ISO week date conventions in a formatting template."
			}
			jsonpathDatetimeDiagnostic(t, jsonpathDatetimeString(tc.input), `$.datetime(`+jsonpathDatetimeString(tc.template)+`)`, d)
		})
	}
	// Ordinary SQL formatting remains permissive; strict JSONPath parsing must
	// not silently replace the shared provider's existing input contract.
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			`SELECT to_date('2023/08/15','YYYY-MM-DD')::text AS changed`,
			`SELECT to_date('2023-08-15trailing','YYYY-MM-DD')::text AS changed`,
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed"}, []string{"TEXT"}, [][]any{{"2023-08-15"}}))
		}
	})
}
