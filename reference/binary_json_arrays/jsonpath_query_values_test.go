package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryValues(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, path, typ string
			rows            [][]any
		}{
			{"jsonb_path_exists", jsonpathQueryPath, "BOOL", jsonpathQueryRows(true)},
			{"jsonb_path_match", `exists(` + jsonpathQueryPath + `)`, "BOOL", jsonpathQueryRows(true)},
			{"jsonb_path_query", jsonpathQueryPath, "JSONB", jsonpathQueryRows(`2`, `3`, `4`)},
			{"jsonb_path_query_first", jsonpathQueryPath, "JSONB", jsonpathQueryRows(`2`)},
			{"jsonb_path_query_array", jsonpathQueryPath, "JSONB", jsonpathQueryRows(`[2, 3, 4]`)},
		} {
			q := `SELECT ` + tc.name + `($1::jsonb,$2::jsonpath,$3::jsonb) AS changed`
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryResult(q, tc.typ, tc.rows, jsonpathQueryDocument, tc.path, jsonpathQueryVars))
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_query_first($1,$2,$3)=jsonb_path_query_array($1,$2,$3)->0 AS changed`, "BOOL", true, jsonpathQueryDocument, jsonpathQueryPath, jsonpathQueryVars))
		// These constructs already belong to the evaluator, not a new query parser.
		for _, tc := range []struct {
			doc, path string
			rows      [][]any
		}{
			{`[[1,2],[3]]`, `$[*][last]`, jsonpathQueryRows(`2`, `3`)},
			{`{"a":{"x":1},"b":{"x":2}}`, `strict $.**{1}.x`, jsonpathQueryRows(`1`, `2`)},
			{`[0,1,2,3]`, `$[1 to 2]`, jsonpathQueryRows(`1`, `2`)},
			{`["A.B","axb"]`, `$[*]?(@ like_regex "a.b" flag "iq")`, jsonpathQueryRows(`"A.B"`)},
			{`[{"x":1},{"x":2}]`, `$[*]?(exists(@.x) && @.x > 1).x`, jsonpathQueryRows(`2`)},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT jsonb_path_query($1,$2) AS changed`, "JSONB", tc.rows, tc.doc, tc.path))
		}
	})
}

func TestReferenceJSONPathQueryNulls(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			doc, path            string
			exists, match, first any
			rows                 [][]any
			array                string
		}{
			{jsonpathQueryDocument, `$.absent`, false, nil, nil, nil, `[]`},
			{jsonpathQueryDocument, `$.nil`, true, nil, `null`, jsonpathQueryRows(`null`), `[null]`},
			{jsonpathQueryDocument, `$.flag`, true, false, `false`, jsonpathQueryRows(`false`), `[false]`},
			{jsonpathQueryDocument, `$.a[*] > 3`, true, true, `true`, jsonpathQueryRows(`true`), `[true]`},
			{jsonpathQueryDocument, `$.a[*] > 9`, true, false, `false`, jsonpathQueryRows(`false`), `[false]`},
			{`"x"`, `$ > 1`, true, nil, `null`, jsonpathQueryRows(`null`), `[null]`},
		} {
			for _, s := range []fixture.Step{
				jsonpathQueryOne(`SELECT jsonb_path_exists($1,$2) AS changed`, "BOOL", tc.exists, tc.doc, tc.path),
				jsonpathQueryOne(`SELECT jsonb_path_match($1,$2,'{}',true) AS changed`, "BOOL", tc.match, tc.doc, tc.path),
				jsonpathQueryOne(`SELECT jsonb_path_query_first($1,$2) AS changed`, "JSONB", tc.first, tc.doc, tc.path),
				jsonpathQueryResult(`SELECT jsonb_path_query($1,$2) AS changed`, "JSONB", tc.rows, tc.doc, tc.path),
				jsonpathQueryOne(`SELECT jsonb_path_query_array($1,$2) AS changed`, "JSONB", tc.array, tc.doc, tc.path),
			} {
				jsonpathNavigateBoth(t, p, b, c, s)
			}
		}
		jsonpathNavigateBoth(t, p, b, c, fixture.Select(`SELECT jsonb_path_query_first('null','$') IS NULL AS sql_null,jsonb_path_query_first('null','$')='null'::jsonb AS json_null,jsonb_path_query_first('{}','$.missing') IS NULL AS absent`, []string{"sql_null", "json_null", "absent"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true}}))
		for _, f := range jsonpathQueryFunctions {
			for slot := range 4 {
				args := []any{`true`, `$`, `{}`, false}
				args[slot] = nil
				rows := jsonpathQueryRows(nil)
				if f.name == "jsonb_path_query" {
					rows = nil
				}
				jsonpathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT `+f.name+`($1,$2,$3,$4) AS changed`, f.typ, rows, args...))
			}
		}
		for _, tc := range []struct{ doc, path string }{{`1`, `$`}, {`{}`, `$.missing`}, {`[true,false]`, `$[*]`}, {`[null,null]`, `$[*]`}, {jsonpathQueryDocument, jsonpathQueryPath}} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_match($1,$2,$3)`, "22038", "single boolean result is expected", tc.doc, tc.path, jsonpathQueryVars))
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match($1,$2,$3,true) AS changed`, "BOOL", nil, tc.doc, tc.path, jsonpathQueryVars))
		}
	})
}

func TestReferenceJSONPathQueryDemand(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryFunctions {
			q := `SELECT ` + f.name + `($1,$2,'{}',$3) AS changed`
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":1},{},{"x":3}]`, `strict $[*].x`, false))
			want := any(nil)
			if f.typ == "JSONB" {
				want = `1`
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(q, f.typ, want, `[{"x":1},{},{"x":3}]`, `strict $[*].x`, true))
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `[1]`, `strict $[0,$v]`, true))
			if f.name != "jsonb_path_exists" {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `[1]`, `$[0,$v]`, true))
			}
		}
		for _, suffix := range []string{" LIMIT 1", ""} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT changed FROM jsonb_path_query('[{"x":1},{}]','strict $[*].x') AS q(changed)`+suffix, "2203A", `JSON object does not contain key "x"`))
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query('[{"x":1},{}]','strict $[*].x')`+suffix, "2203A", `JSON object does not contain key "x"`))
		}
		for _, tc := range []struct {
			doc  string
			want any
		}{
			{`[{"x":true},{},{"x":false}]`, true}, {`[{"x":false},{}]`, false}, {`[{}, {"x":true}]`, nil}, {`[{"x":true},{"x":false},{}]`, nil},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_match($1,'strict $[*].x','{}',true) AS changed`, "BOOL", tc.want, tc.doc))
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT jsonb_path_exists('[1]','$[0,$v]','{}',true) AS changed`, "BOOL", true))
		jsonpathNavigateBoth(t, p, b, c, jsonpathQueryResult(`SELECT jsonb_path_query('[{"x":1},{},{"x":3}]','$[*].x') AS changed`, "JSONB", jsonpathQueryRows(`1`, `3`)))
		for _, f := range jsonpathQueryFunctions {
			want := any(nil)
			if f.name == "jsonb_path_exists" {
				want = true
			}
			if f.typ == "JSONB" {
				want = `null`
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+f.name+`('[{"x":1},{}]','strict exists($[*].x)') AS changed`, f.typ, want))
			// Capability boundary: PG evaluates arithmetic; native must retain 0A000.
			s := jsonpathQueryOne(`SELECT `+f.name+`('1','$ + 1','{}',true) AS changed`, f.typ, `2`)
			if f.name == "jsonb_path_exists" {
				s = jsonpathQueryOne(s.Query, "BOOL", true)
			}
			if f.name == "jsonb_path_match" {
				s = jsonpathQueryOne(`SELECT jsonb_path_match('1','$ + 1 == 2','{}',true) AS changed`, "BOOL", true)
			}
			if !b.PG {
				s = jsonpathNavigateError(s.Query, "0A000", "stuntgres: JSONPath arithmetic is unsupported")
			}
			jsonpathNavigateBoth(t, p, b, c, s)
		}
	})
}
