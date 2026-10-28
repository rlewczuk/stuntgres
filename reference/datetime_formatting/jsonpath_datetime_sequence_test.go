package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeSequences(t *testing.T) {
	for _, tc := range []struct{ doc, path, want string }{
		{`["2023-08-15","2023-08-16"]`, `$.date()`, `["2023-08-15", "2023-08-16"]`},
		{`[]`, `$.date()`, `[]`}, {`{"a":[]}`, `$.a[*].date()`, `[]`},
		{`["2023-08-15","2023-08-16"]`, `$[0,0,1].date()`, `["2023-08-15", "2023-08-15", "2023-08-16"]`},
	} {
		t.Run(tc.doc+tc.path, func(t *testing.T) { jsonpathDatetimeValue(t, tc.doc, tc.path, tc.want) })
	}
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stmt, err := c.PrepareContext(p.Ctx, jsonpathDatetimeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: jsonpathDatetimeSQL}
		d := jsonpathDatetimeFormatError("date", "bad")
		bad := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, d, `["2023-08-15","bad","2023-08-16"]`, `$[*].date()`, `{}`, false)
		bad.SelectRows = true
		for _, s := range []fixture.Step{
			bad,
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `["2023-08-15"]`, `["2023-08-15","bad","2023-08-16"]`, `$[*].date()`, `{}`, true),
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `["18:40:00"]`, `"18:40"`, `$.datetime("HH24:MI")`, `{}`, false),
			jsonpathNavigateSelect(jsonpathDatetimeSQL, `[]`, `[]`, `$[*].date()`, `{}`, false),
			jsonpathNavigateSelect(jsonpathDatetimeSQL, nil, nil, `$.date()`, `{}`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
	// Query-first materializes the temporal sequence even in lax mode; exists
	// can stop after the first lax match. Give each consumer/mode/silent cell
	// its own scenario so an unresolved tail diagnostic cannot hide the others.
	for _, mode := range []string{"lax", "strict"} {
		for _, consumer := range []string{"jsonb_path_query_first", "jsonb_path_exists"} {
			for _, silent := range []bool{false, true} {
				name := mode + "/" + consumer + "/errors"
				if silent {
					name = mode + "/" + consumer + "/silent"
				}
				t.Run(name, func(t *testing.T) {
					jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						q := `SELECT ` + consumer + `($1,$2,'{}',$3) AS changed`
						args := []any{`["2023-08-15","bad"]`, mode + ` $[*].date()`, silent}
						var s fixture.Step
						if !silent && (consumer == "jsonb_path_query_first" || mode == "strict") {
							s = fixture.ErrorDiagnostic(q, jsonpathDatetimeFormatError("date", "bad"), args...)
							s.SelectRows = true
						} else if consumer == "jsonb_path_query_first" {
							s = jsonpathNavigateSelect(q, `"2023-08-15"`, args...)
						} else {
							var want any = true
							if mode == "strict" {
								want = nil
							}
							s = fixture.Select(q, []string{"changed"}, []string{"BOOL"}, [][]any{{want}}, args...)
						}
						fixture.JSONPathNavigateBoth(t, p, b, c, s)
					})
				})
			}
		}
	}
}

func TestReferenceJSONPathDatetimeConsumers(t *testing.T) {
	for _, tc := range []struct {
		doc, path    string
		array, first any
		rows         [][]any
		exists       bool
	}{
		{`[]`, `$[*].date()`, `[]`, nil, nil, false},
		{`"2023-08-15"`, `$.date()`, `["2023-08-15"]`, `"2023-08-15"`, [][]any{{`"2023-08-15"`}}, true},
		{`["2023-08-15","2023-08-16"]`, `$[*].date()`, `["2023-08-15", "2023-08-16"]`, `"2023-08-15"`, [][]any{{`"2023-08-15"`}, {`"2023-08-16"`}}, true},
		{`null`, `$`, `[null]`, `null`, [][]any{{`null`}}, true},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, s := range []fixture.Step{
					jsonpathNavigateSelect(`SELECT jsonb_path_query_array($1,$2) AS changed`, tc.array, tc.doc, tc.path),
					jsonpathNavigateSelect(`SELECT jsonb_path_query_first($1,$2) AS changed`, tc.first, tc.doc, tc.path),
					fixture.Select(`SELECT jsonb_path_query($1,$2) AS changed`, []string{"changed"}, []string{"JSONB"}, tc.rows, tc.doc, tc.path),
					fixture.Select(`SELECT jsonb_path_exists($1,$2) AS changed`, []string{"changed"}, []string{"BOOL"}, [][]any{{tc.exists}}, tc.doc, tc.path),
					fixture.Select(`SELECT $1::jsonb @? $2::jsonpath AS changed`, []string{"changed"}, []string{"BOOL"}, [][]any{{tc.exists}}, tc.doc, tc.path),
				} {
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
			})
		})
	}
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, path := range []string{`$.date()`, `$[*].date()`} {
			q := `SELECT jsonb_path_match('["2023-08-15","2023-08-16"]',$1) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22038", "single boolean result is expected", path))
		}
		for _, q := range []string{`SELECT jsonb_path_match($1,$2) AS changed`, `SELECT $1::jsonb @@ $2::jsonpath AS changed`} {
			for _, tc := range []struct {
				doc, path string
				want      any
			}{{`"2023-08-15"`, `$.date() == $.date()`, true}, {`"2023-08-15"`, `$.date() == $`, nil}} {
				fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.doc, tc.path))
			}
		}
		for _, fn := range []string{"jsonb_path_query_array", "jsonb_path_query_first", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query"} {
			for slot := range 4 {
				args := []any{`"2023-08-15"`, `$.date()`, `{}`, false}
				args[slot] = nil
				q := `SELECT ` + fn + `($1::jsonb,$2::jsonpath,$3::jsonb,$4::boolean) AS changed`
				typ := "JSONB"
				if fn == "jsonb_path_exists" || fn == "jsonb_path_match" {
					typ = "BOOL"
				}
				rows := [][]any{{nil}}
				if fn == "jsonb_path_query" {
					rows = nil
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed"}, []string{typ}, rows, args...))
			}
		}
		missing := fixture.ControlsReferenceDiagnostic("42704", `could not find jsonpath variable "missing"`)
		s := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, missing, `"2023-08-15"`, `$missing.date()`, `{}`, true)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
	})
}
