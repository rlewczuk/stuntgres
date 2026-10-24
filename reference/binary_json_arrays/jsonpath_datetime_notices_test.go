package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeSQLJSONNotices(t *testing.T) {
	for _, tc := range []struct{ method, input, output, message string }{
		{"time", "12:34:56.123456", "12:34:56.123456", "TIME(10) precision reduced to maximum allowed, 6"},
		{"time_tz", "12:34:56.123456+05:30", "12:34:56.123456+05:30", "TIME(10) WITH TIME ZONE precision reduced to maximum allowed, 6"},
		{"timestamp", "2023-08-15 12:34:56.123456", "2023-08-15T12:34:56.123456", "TIMESTAMP(10) precision reduced to maximum allowed, 6"},
		{"timestamp_tz", "2023-08-15 12:34:56.123456+05:30", "2023-08-15T12:34:56.123456+05:30", "TIMESTAMP(10) WITH TIME ZONE precision reduced to maximum allowed, 6"},
	} {
		path := `$[*].` + tc.method + `(10)`
		for _, route := range []struct {
			name, query string
			rows        [][]any
		}{
			{"query", `SELECT JSON_QUERY($1::jsonb,` + jsonReferenceQuote(path) + ` WITH UNCONDITIONAL WRAPPER ERROR ON ERROR) AS changed`, [][]any{{"[" + jsonpathDatetimeString(tc.output) + ", " + jsonpathDatetimeString(tc.output) + "]"}}},
			{"table-row", `SELECT j.changed FROM JSON_TABLE($1::jsonb,` + jsonReferenceQuote(path) + ` COLUMNS(changed jsonb FORMAT JSON PATH '$' ERROR ON ERROR)) AS j`, [][]any{{jsonpathDatetimeString(tc.output)}, {jsonpathDatetimeString(tc.output)}}},
			{"table-column", `SELECT j.changed FROM JSON_TABLE($1::jsonb,'$' COLUMNS(changed jsonb FORMAT JSON PATH ` + jsonReferenceQuote(path) + ` WITH UNCONDITIONAL WRAPPER ERROR ON ERROR)) AS j`, [][]any{{"[" + jsonpathDatetimeString(tc.output) + ", " + jsonpathDatetimeString(tc.output) + "]"}}},
		} {
			t.Run(tc.method+"/"+route.name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: tc.message}
						want := []fixture.Diagnostic{warning, warning}
						doc := "[" + jsonpathDatetimeString(tc.input) + "," + jsonpathDatetimeString(tc.input) + "]"
						for _, prepared := range []bool{false, true} {
							s := fixture.Select(route.query, []string{"changed"}, []string{"JSONB"}, route.rows, doc)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, conn, record, s)
							if !reflect.DeepEqual(notices, want) {
								t.Fatalf("prepared=%t complete notices=%#v want=%#v", prepared, notices, want)
							}
							notices = nil
						}
					})
				}
			})
		}
	}
}
