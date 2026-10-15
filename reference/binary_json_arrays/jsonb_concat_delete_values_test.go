package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ left, right, want string }{
		{`{"a":{"x":1},"b":2}`, `{"a":{"y":3}}`, `{"a": {"y": 3}, "b": 2}`},
		{`{"a":1}`, `{"a":null}`, `{"a": null}`},
		{`[1,[2]]`, `[[3],4]`, `[1, [2], [3], 4]`},
		{`3`, `4`, `[3, 4]`}, {`null`, `[1]`, `[null, 1]`},
		{`{}`, `[]`, `[{}]`}, {`[]`, `{}`, `[{}]`},
		{`3`, `[]`, `[3]`}, {`[3]`, `{}`, `[3, {}]`},
		{`{}`, `{}`, `{}`}, {`[]`, `[]`, `[]`},
		{`true`, `"x"`, `[true, "x"]`},
		{`{"é":1,"aa":2}`, `{"a":1.00,"é":3}`, `{"a": 1.00, "aa": 2, "é": 3}`},
	} {
		t.Run("concat/"+tc.left+tc.right, func(t *testing.T) {
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT $1::jsonb || $2::jsonb AS v", tc.want, tc.left, tc.right))
		})
	}
	for _, tc := range []struct{ doc, key, want string }{
		{`{"a":null,"b":1.00}`, "a", `{"b": 1.00}`},
		{`{"a":1}`, "missing", `{"a": 1}`}, {`{}`, "a", `{}`},
		{`["b",1,"b",["b"],{"b":2},null]`, "b", `[1, ["b"], {"b": 2}, null]`},
		{`["1",1]`, "1", `[1]`}, {`[]`, "x", `[]`},
		{`["é","e","é"]`, "é", `["e"]`},
		{`{"":1,"é":2}`, "", `{"é": 2}`},
	} {
		t.Run("text/"+tc.doc+tc.key, func(t *testing.T) {
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT $1::jsonb - $2::text AS v", tc.want, tc.doc, tc.key))
		})
	}
	for _, tc := range []struct {
		index int64
		want  string
	}{
		{0, `[2, 3]`}, {1, `[1, 3]`}, {2, `[1, 2]`}, {-1, `[1, 2]`}, {-3, `[2, 3]`},
		{-4, `[1, 2, 3]`}, {3, `[1, 2, 3]`}, {-2147483648, `[1, 2, 3]`}, {2147483647, `[1, 2, 3]`},
	} {
		jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT $1::jsonb - $2::integer AS v", tc.want, `[1,2,3]`, tc.index))
	}
	jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT '[]'::jsonb - -1 AS v", `[]`))
	jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect(`SELECT '["1",1]'::jsonb - 1 AS v`, `["1"]`))
	for _, q := range []string{
		`SELECT NULL::jsonb || '{}'::jsonb AS v`, `SELECT '{}'::jsonb || NULL::jsonb AS v`,
		`SELECT NULL::jsonb - 1 AS v`, `SELECT 'null'::jsonb - NULL::integer AS v`,
		`SELECT '1'::jsonb - NULL::text AS v`,
	} {
		jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect(q, nil))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbcd_owned(doc jsonb)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbcd_owned VALUES('{"a":1,"b":2}')`, 1))
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT doc,doc - 'a' AS a,doc || '{"a":3}' AS b FROM jbcd_owned`, []string{"doc", "a", "b"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{`{"a": 1, "b": 2}`, `{"b": 2}`, `{"a": 3, "b": 2}`}}))
}

func TestReferenceJSONBConcatDeleteValues(t *testing.T) {
	jsonbConcatDeleteScenario(t, jsonbConcatDeleteValues)
}
