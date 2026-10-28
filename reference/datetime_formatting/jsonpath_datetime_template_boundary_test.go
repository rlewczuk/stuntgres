package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonpathDatetimeReviewStep(t *testing.T, s fixture.Step) {
	t.Helper()
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
	})
}

func jsonpathDatetimeReviewError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceJSONPathDatetimeTemplateCompilation(t *testing.T) {
	for _, tc := range []struct{ name, input, template, invalid string }{
		{"quoted-alpha", "2023X08X15", `YYYY"X"MM"X"DD`, ""},
		{"unquoted-alpha", "2023X08X15", `YYYYXMMXDD`, "X"},
		{"quoted-utf8", "2023年08月15日", `YYYY"年"MM"月"DD"日"`, ""},
		{"unquoted-utf8", "2023年08月15日", `YYYY年MM月DD日`, "年"},
		{"quoted-trailing-alpha", "2023-08-15X", `YYYY-MM-DD"X"`, ""},
		{"unquoted-trailing-alpha", "2023-08-15X", `YYYY-MM-DDX`, "X"},
		{"unquoted-trailing-alpha-short-input", "2023-08-15", `YYYY-MM-DDX`, "X"},
		{"quoted-trailing-utf8", "2023-08-15年", `YYYY-MM-DD"年"`, ""},
		{"unquoted-trailing-utf8", "2023-08-15年", `YYYY-MM-DD年`, "年"},
		{"unquoted-trailing-utf8-short-input", "2023-08-15", `YYYY-MM-DD年`, "年"},
		{"invalid-before-input-parse", "bad", `YYYY_MM_DD`, "_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := `$.datetime(` + jsonpathDatetimeString(tc.template) + `)`
			var s fixture.Step
			if tc.invalid == "" {
				s = jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-15"]`, jsonpathDatetimeString(tc.input), path, `{}`, false)
			} else {
				d := fixture.ControlsReferenceDiagnostic("22007", `invalid datetime format separator: "`+tc.invalid+`"`)
				s = jsonpathDatetimeReviewError(jsonpathDatetimeSQL, d, jsonpathDatetimeString(tc.input), path, `{}`, false)
			}
			jsonpathDatetimeReviewStep(t, s)
		})
	}
	for _, tc := range []struct{ input, template string }{
		{"2023X08X15", `YYYYXMMXDD`},
		{"2023年08月15日", `YYYY年MM月DD日`},
		{"2023-08-15X", `YYYY-MM-DDX`},
	} {
		t.Run("ordinary/"+tc.template, func(t *testing.T) {
			jsonpathDatetimeReviewStep(t, fixture.Select(`SELECT to_date($1::text,$2::text)::text AS changed`, []string{"changed"}, []string{"TEXT"}, [][]any{{"2023-08-15"}}, tc.input, tc.template))
		})
	}
}

func TestReferenceJSONPathDatetimeTemplateErrorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, input, template, message string
		compile                        bool
	}{
		{"invalid-template", "2023_08_15", "YYYY_MM_DD", `invalid datetime format separator: "_"`, true},
		{"invalid-template-short-input", "bad", "YYYY_MM_DD", `invalid datetime format separator: "_"`, true},
		{"input-separator-mismatch", "2023/08/15", "YYYY-MM-DD", `unmatched format separator "-"`, false},
	} {
		path := `$.datetime(` + jsonpathDatetimeString(tc.template) + `)`
		doc := jsonpathDatetimeString(tc.input)
		d := fixture.ControlsReferenceDiagnostic("22007", tc.message)
		for _, silent := range []bool{false, true} {
			name := "errors"
			if silent {
				name = "silent"
			}
			t.Run(tc.name+"/array/"+name, func(t *testing.T) {
				if tc.compile || !silent {
					jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(jsonpathDatetimeSQL, d, doc, path, `{}`, silent))
				} else {
					jsonpathDatetimeReviewStep(t, jsonpathNavigateSelect(jsonpathDatetimeSQL, `[]`, doc, path, `{}`, true))
				}
			})
			for _, predicate := range []struct{ name, path, want string }{
				{"exists-predicate", `exists(` + path + `)`, `[null]`},
				{"filter-predicate", `$ ? (exists(@.datetime(` + jsonpathDatetimeString(tc.template) + `)))`, `[]`},
			} {
				t.Run(tc.name+"/"+predicate.name+"/"+name, func(t *testing.T) {
					if tc.compile {
						jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(jsonpathDatetimeSQL, d, doc, predicate.path, `{}`, silent))
					} else {
						jsonpathDatetimeReviewStep(t, jsonpathNavigateSelect(jsonpathDatetimeSQL, predicate.want, doc, predicate.path, `{}`, silent))
					}
				})
			}
		}
		for _, op := range []string{"@?", "@@"} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				q := `SELECT $1::jsonb ` + op + ` $2::jsonpath AS changed`
				if tc.compile {
					jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(q, d, doc, path))
				} else {
					jsonpathDatetimeReviewStep(t, fixture.Select(q, []string{"changed"}, []string{"BOOL"}, [][]any{{nil}}, doc, path))
				}
			})
		}
		for _, route := range []struct {
			name, query, typ string
			rows             [][]any
			throws           bool
		}{
			{"query-null", `SELECT JSON_QUERY($1::jsonb,$2::jsonpath NULL ON ERROR) AS changed`, "JSONB", [][]any{{nil}}, false},
			{"query-default", `SELECT JSON_QUERY($1::jsonb,$2::jsonpath DEFAULT '"fallback"'::jsonb ON ERROR) AS changed`, "JSONB", [][]any{{`"fallback"`}}, false},
			{"query-error", `SELECT JSON_QUERY($1::jsonb,$2::jsonpath ERROR ON ERROR) AS changed`, "JSONB", nil, true},
			{"value-default", `SELECT JSON_VALUE($1::jsonb,$2::jsonpath DEFAULT 'fallback' ON ERROR) AS changed`, "TEXT", [][]any{{"fallback"}}, false},
			{"value-error", `SELECT JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON ERROR) AS changed`, "TEXT", nil, true},
			{"exists-true", `SELECT JSON_EXISTS($1::jsonb,$2::jsonpath TRUE ON ERROR) AS changed`, "BOOL", [][]any{{true}}, false},
			{"exists-error", `SELECT JSON_EXISTS($1::jsonb,$2::jsonpath ERROR ON ERROR) AS changed`, "BOOL", nil, true},
		} {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				if tc.compile || route.throws {
					jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(route.query, d, doc, path))
				} else {
					jsonpathDatetimeReviewStep(t, fixture.Select(route.query, []string{"changed"}, []string{route.typ}, route.rows, doc, path))
				}
			})
		}
		for _, route := range []struct {
			name, query string
			rows        [][]any
		}{
			{"table-row", `SELECT j.changed FROM JSON_TABLE($1::jsonb,` + fixture.JSONReferenceQuote(path) + ` COLUMNS(changed jsonb FORMAT JSON PATH '$') EMPTY ON ERROR) AS j`, nil},
			{"table-column", `SELECT j.changed FROM JSON_TABLE($1::jsonb,'$' COLUMNS(changed jsonb FORMAT JSON PATH ` + fixture.JSONReferenceQuote(path) + ` NULL ON ERROR)) AS j`, [][]any{{nil}}},
		} {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				if tc.compile {
					jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(route.query, d, doc))
				} else {
					jsonpathDatetimeReviewStep(t, fixture.Select(route.query, []string{"changed"}, []string{"JSONB"}, route.rows, doc))
				}
			})
		}
	}
	// An invalid template remains undemanded when there is no candidate, a
	// formal SQL NULL, or a short-circuited predicate. Compilation is not a
	// statement-wide preflight that can move errors ahead of normal demand.
	for _, tc := range []struct {
		name, doc, path string
		want            any
		sqlnull         bool
	}{
		{"empty-array", `[]`, `$.datetime("YYYY_MM_DD")`, `[]`, false},
		{"sql-null", "", `$.datetime("YYYY_MM_DD")`, nil, true},
		{"short-circuit", `"bad"`, `(false == true) && exists($.datetime("YYYY_MM_DD"))`, `[false]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc any = tc.doc
			if tc.sqlnull {
				doc = nil
			}
			jsonpathDatetimeReviewStep(t, jsonpathNavigateSelect(jsonpathDatetimeSQL, tc.want, doc, tc.path, `{}`, false))
		})
	}
}

func TestReferenceJSONPathDatetimeRawDiagnostics(t *testing.T) {
	for _, method := range []string{"datetime", "date", "time", "time_tz", "timestamp", "timestamp_tz"} {
		for _, tc := range []struct{ name, input string }{
			{"quote", `bad"date`}, {"backslash", `bad\date`}, {"tab", "bad\tdate"}, {"newline", "bad\ndate"}, {"carriage-return", "bad\rdate"}, {"control", "bad\x01date"},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				// JSON encoding belongs only to the input argument. The complete
				// diagnostic embeds the decoded input unchanged between quotes.
				d := jsonpathDatetimeFormatError(method, tc.input)
				jsonpathDatetimeReviewStep(t, jsonpathDatetimeReviewError(jsonpathDatetimeSQL, d, jsonpathDatetimeString(tc.input), `$.`+method+`()`, `{}`, false))
			})
		}
	}
}
