package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeKinds(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, tc := range []struct{ input, kind string }{
			{`{}`, "object"}, {`[]`, "array"}, {`"null"`, "string"}, {`""`, "string"},
			{`-123.4`, "number"}, {`1e0`, "number"}, {`true`, "boolean"}, {`false`, "boolean"}, {`null`, "null"},
			{`{"a":[null,1]}`, "object"}, {`[{}]`, "array"},
		} {
			name := family + "_typeof"
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+name+"("+jsonTypeQuote(tc.input)+"::"+family+")", []string{name}, []string{"TEXT"}, [][]any{{tc.kind}}))
		}
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+family+"_typeof(NULL::"+family+") AS k, "+family+"_typeof('null') IS NULL AS jn, "+family+"_typeof(NULL) IS NULL AS sn", []string{"k", "jn", "sn"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{nil, false, true}}))
	}
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT json_typeof('1e131072'::json) AS k, json_typeof('{"x":"\u0000"}'::json) AS o`, []string{"k", "o"}, []string{"TEXT", "TEXT"}, [][]any{{"number", "object"}}))
}

func TestReferenceJSONTypeKinds(t *testing.T) { jsonTypeScenario(t, jsonTypeKinds) }

func jsonTypeLengths(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, tc := range []struct {
			input  string
			length int64
		}{
			{`[]`, 0}, {`[1,2,3,{"f1":1,"f2":[5,6]},4]`, 5}, {`[null,[],{},"a,b",[1,2]]`, 5}, {`["a,\\\"b",{"a":[1,2]}]`, 2},
		} {
			name := family + "_array_length"
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+name+"("+jsonTypeQuote(tc.input)+"::"+family+")", []string{name}, []string{"INT4"}, [][]any{{tc.length}}))
		}
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT "+family+"_array_length(NULL) AS n", []string{"n"}, []string{"INT4"}, [][]any{{nil}}))
	}
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT json_array_length('["\u0000",1e131072]'::json) AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
}

func TestReferenceJSONTypeLengths(t *testing.T) { jsonTypeScenario(t, jsonTypeLengths) }
