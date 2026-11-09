package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified canonical values are confirmed against pinned PostgreSQL 18.0.
func TestReferenceJSONBStripNullsValues(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, ordinary, arrays string }{
			{jsonbStripInput, jsonbStripDefault, jsonbStripArrays},
			{`[null,null,{"a":null},[],[null,null],null]`, `[null, null, {}, [], [null, null], null]`, `[{}, [], []]`},
			{`{"a":{"b":null},"d":{}}`, `{"a": {}, "d": {}}`, `{"a": {}, "d": {}}`},
			{`[null]`, `[null]`, `[]`}, {`{"a":null}`, `{}`, `{}`},
			{`[null,0,false,"","null",2,2,null]`, `[null, 0, false, "", "null", 2, 2, null]`, `[0, false, "", "null", 2, 2]`},
			{`null`, `null`, `null`}, {`"null"`, `"null"`, `"null"`},
			{`1.00`, `1.00`, `1.00`}, {`true`, `true`, `true`}, {`false`, `false`, `false`}, {`{}`, `{}`, `{}`}, {`[]`, `[]`, `[]`},
			{`{"k":1,"k":null}`, `{}`, `{}`}, {`{"k":null,"k":2}`, `{"k": 2}`, `{"k": 2}`},
			{`{"a":1,"\u0061":null}`, `{}`, `{}`},
			{`{"é":null,"aaa":1.2300,"z":"a'b\\c","a":"\n","face":"\uD83D\uDE00"}`, `{"a": "\n", "z": "a'b\\c", "aaa": 1.2300, "face": "😀"}`, `{"a": "\n", "z": "a'b\\c", "aaa": 1.2300, "face": "😀"}`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, option := range []struct{ suffix, want string }{{"", tc.ordinary}, {",false", tc.ordinary}, {",true", tc.arrays}} {
					for _, target := range []string{jsonTypeQuote(tc.input), jsonTypeQuote(tc.input) + "::jsonb", "CAST(" + jsonTypeQuote(tc.input) + " AS jsonb)"} {
						jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls("+target+option.suffix+") AS stripped", option.want))
					}
					jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls($1"+option.suffix+") AS stripped", option.want, tc.input))
				}
			})
		}
		for _, args := range []string{"NULL", "NULL,false", "NULL,true", "NULL,NULL", "'{}',NULL", "'null',NULL"} {
			jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls("+args+") AS stripped", nil))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_values(id int,v jsonb,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO jbs_values VALUES(1,$1,false),(2,$1,true)", 2, jsonbStripInput)))
		original := `{"a": 1, "b": null, "c": [2, null, 3], "d": {"e": 4, "f": null}}`
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,v,jsonb_strip_nulls(v,flag) AS stripped FROM jbs_values ORDER BY id", []string{"id", "v", "stripped"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(1), original, jsonbStripDefault}, {int64(2), original, jsonbStripArrays}}))
		for _, target := range []string{`jsonb_extract_path('{"doc":{"a":null,"b":[null,1]}}','doc')`, `jsonb_build_object('a',NULL,'b',jsonb_build_array(NULL,1))`} {
			jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls("+target+",true) AS stripped", `{"b": [1]}`))
		}
	})
}
