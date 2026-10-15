package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbExtractPaths(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct {
		name      string
		doc, path any
		parts     []string
		raw, text any
	}{
		{"nested", `{"a":[{"b":7}]}`, "{a,0,b}", []string{"a", "0", "b"}, "7", "7"},
		{"negative", `{"a":[{"b":7}]}`, "{a,-1,b}", []string{"a", "-1", "b"}, "7", "7"},
		{"multidimensional", `{"a":[{"b":{"c":9}}]}`, "{{a,0},{b,c}}", nil, "9", "9"},
		{"lower_bound", `{"a":[{"b":7}]}`, "[0:2]={a,0,b}", nil, "7", "7"},
		{"null_array", `{"a":7}`, nil, nil, nil, nil},
		{"null_element", `{"a":7}`, "{a,NULL}", nil, nil, nil},
		{"null_after_missing", `{}`, "{missing,NULL}", nil, nil, nil},
		{"literal_NULL", `{"NULL":7}`, `{"NULL"}`, []string{"NULL"}, "7", "7"},
		{"sql_null", nil, "{a}", []string{"a"}, nil, nil},
		{"empty_object", `{}`, "{a}", []string{"a"}, nil, nil},
		{"empty_array", `[]`, "{0}", []string{"0"}, nil, nil},
		{"scalar_continuation", `{"a":7}`, "{a,b}", []string{"a", "b"}, nil, nil},
		{"duplicate_intermediate", `{"a":{"b":1},"a":{}}`, "{a,b}", []string{"a", "b"}, nil, nil},
		{"unrelated_sibling", `{"x":[0],"a":[10,20,30]}`, "{a,-1}", []string{"a", "-1"}, "30", "30"},
	} {
		t.Run(tc.name, func(t *testing.T) { jsonbExtractPathForms(t, p, b, c, tc.doc, tc.path, tc.parts, tc.raw, tc.text) })
	}
	for _, tc := range []struct {
		doc       string
		raw, text any
	}{
		{`{"z":1,"z":2,"aa":1.230e-5}`, `{"z": 2, "aa": 0.00001230}`, `{"z": 2, "aa": 0.00001230}`},
		{`"x\ny"`, `"x\ny"`, "x\ny"}, {"null", "null", nil}, {"7.00", "7.00", "7.00"},
		{"true", "true", "true"}, {"false", "false", "false"}, {`""`, `""`, ""}, {"[]", "[]", "[]"}, {"{}", "{}", "{}"},
	} {
		t.Run("empty/"+tc.doc, func(t *testing.T) { jsonbExtractPathForms(t, p, b, c, tc.doc, "{}", nil, tc.raw, tc.text) })
		if tc.doc == "null" || tc.doc == "7.00" || tc.doc == "true" || tc.doc == `"x\ny"` {
			for _, part := range []string{"0", "-1"} {
				t.Run("scalar_path/"+tc.doc+part, func(t *testing.T) { jsonbExtractPathForms(t, p, b, c, tc.doc, "{"+part+"}", []string{part}, nil, nil) })
			}
		}
	}
	// Extraction accepts strtol-style indices and never raises mutator errors.
	for _, tc := range []struct {
		part string
		want any
	}{
		{"01", "20"}, {"+1", "20"}, {" 1", "20"}, {"\t1", "20"}, {"\n1", "20"}, {"-0", "10"},
		{"", nil}, {"1 ", nil}, {"1x", nil}, {"1.0", nil}, {"0x1", nil}, {"1_0", nil},
		{"2147483648", nil}, {"-2147483649", nil}, {"2147483647", nil}, {"-2147483648", nil}, {"3", nil}, {"-4", nil},
	} {
		t.Run("index_parser/"+tc.part, func(t *testing.T) {
			// Build arrays in SQL so tabs/newlines/empty strings are unambiguous.
			for _, f := range []struct{ op, fn, typ string }{{"#>", "jsonb_extract_path", "JSONB"}, {"#>>", "jsonb_extract_path_text", "TEXT"}} {
				for _, q := range []string{"SELECT $1::jsonb " + f.op + " ARRAY[$2::text] AS v", "SELECT " + f.fn + "($1,$2) AS v", "SELECT " + f.fn + "($1,VARIADIC ARRAY[$2::text]) AS v"} {
					jsonExtractBoth(t, p, b, c, jsonExtractOne(q, f.typ, tc.want, "[10,20,30]", tc.part))
				}
				jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb "+f.op+" ARRAY[$2::text] AS v", f.typ, "9", "{"+jsonbExtractJSONKey(tc.part)+":9}", tc.part))
			}
		})
	}
}

func TestReferenceJSONBExtractPaths(t *testing.T) { jsonbExtractScenario(t, jsonbExtractPaths) }
