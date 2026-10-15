package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractPaths(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct {
		doc, path string
		parts     []string
		raw, text any
	}{
		{`{"a":[{"b":7}]}`, "{a,0,b}", []string{"a", "0", "b"}, "7", "7"},
		{`{"a":[10,20,30]}`, "{a,-1}", []string{"a", "-1"}, "30", "30"},
		{`{"a":1}`, "{a,x}", []string{"a", "x"}, nil, nil},
		{`{"a":null}`, "{a,x}", []string{"a", "x"}, nil, nil},
		{`[1]`, "{missing}", []string{"missing"}, nil, nil},
		{`{"a":[{"b":{"c":9}}]}`, "{{a,0},{b,c}}", nil, "9", "9"},
		{`{"a":[{"b":7}]}`, "[0:2]={a,0,b}", nil, "7", "7"},
		{`{"a":1}`, "{missing,NULL}", nil, nil, nil},
		{`{"a":1}`, "{NULL,a}", nil, nil, nil},
	} {
		t.Run(tc.doc+tc.path, func(t *testing.T) { jsonExtractPathForms(t, p, b, c, tc.doc, tc.path, tc.parts, tc.raw, tc.text) })
	}
	for _, part := range []string{"01", "+1", "-0", " 1", "\t1", "", "1 ", "1x", "1.0", "0x1", "1_0", "2147483648", "-2147483649", "-2147483648"} {
		var want any
		switch part {
		case "01", "+1", " 1", "\t1":
			want = "20"
		case "-0":
			want = "10"
		}
		path := "{" + `"` + part + `"` + "}"
		jsonExtractPathForms(t, p, b, c, "[10,20,30]", path, []string{part}, want, want)
		// Strings rejected as array indexes remain exact object keys.
		if part != "\t1" {
			jsonExtractPathForms(t, p, b, c, `{"`+part+`":8}`, path, []string{part}, "8", "8")
		}
	}
	for _, q := range []string{"SELECT $1::json #> $2::text[] AS v", "SELECT json_extract_path($1,VARIADIC $2::text[]) AS v"} {
		for _, args := range [][]any{{nil, "{a}"}, {`{"a":1}`, nil}} {
			jsonExtractBoth(t, p, b, c, jsonExtractOne(q, "JSON", nil, args...))
		}
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT json_extract_path(from_json => '{"a":7}'::json,VARIADIC path_elems => '{a}'::text[]) AS v`, "JSON", "7"))
}
func TestReferenceJSONExtractPaths(t *testing.T) { jsonExtractScenario(t, jsonExtractPaths) }

func jsonExtractReleasePaths(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	// PostgreSQL 18.0 retains these release-specific traversal outcomes.
	for _, tc := range []struct {
		doc  string
		want any
	}{
		{`{"a":{"b":1},"a":{}}`, "1"},
		{`{"a":{},"a":{"b":2}}`, "2"},
		{`{"a":{"b":1},"a":{"b":null}}`, "null"},
		{`{"a":{"b":1},"a":0}`, "1"},
	} {
		var text any = tc.want
		if text == "null" {
			text = nil
		}
		t.Run(tc.doc, func(t *testing.T) {
			jsonExtractPathForms(t, p, b, c, tc.doc, "{a,b}", []string{"a", "b"}, tc.want, text)
		})
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT ('{"a":{"b":1},"a":{}}'::json -> 'a') -> 'b' AS v`, "JSON", nil))
	for _, tc := range []struct{ doc, want string }{
		{`{"x":[0],"a":[10,20,30]}`, "10"},
		{`{"x":[0,1],"a":[10,20,30]}`, "20"},
		{`{"a":[10,20,30],"x":[0]}`, "30"},
		{`{"x":{"y":[0]},"a":[10,20,30]}`, "30"},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			jsonExtractPathForms(t, p, b, c, tc.doc, "{a,-1}", []string{"a", "-1"}, tc.want, tc.want)
		})
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT ('{"x":[0],"a":[10,20,30]}'::json -> 'a') -> -1 AS v`, "JSON", "30"))
	for _, tc := range []struct {
		doc       string
		raw, text any
	}{
		{" \n { \"a\":1 } \n", `{ "a":1 }`, `{ "a":1 }`},
		{" \n [ 1 ] \n", "[ 1 ]", "[ 1 ]"},
		{" \n 1e+09 \n", " \n 1e+09", " \n 1e+09"},
		{"  true  ", "  true", "  true"},
		{`  "x\ny"  `, `  "x\ny"`, "x\ny"},
		{"  null  ", "  null", nil},
	} {
		t.Run(tc.doc, func(t *testing.T) { jsonExtractPathForms(t, p, b, c, tc.doc, "{}", nil, tc.raw, tc.text) })
	}
}
func TestReferenceJSONExtractReleasePaths(t *testing.T) {
	jsonExtractScenario(t, jsonExtractReleasePaths)
}
