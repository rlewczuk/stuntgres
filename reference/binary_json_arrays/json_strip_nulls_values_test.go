package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsValues(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ suffix, want string }{{"", jsonStripDefault}, {",false", jsonStripDefault}, {",true", jsonStripArrays}} {
			for _, target := range []string{jsonTypeQuote(jsonStripInput), jsonTypeQuote(jsonStripInput) + "::json", "CAST(" + jsonTypeQuote(jsonStripInput) + " AS json)"} {
				for _, name := range []string{"json_strip_nulls", "pg_catalog.json_strip_nulls"} {
					jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT "+name+"("+target+tc.suffix+") AS stripped", tc.want))
				}
			}
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_values(id int,v json,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO js_values VALUES(1,$1,false),(2,$1,true)", 2, jsonStripInput)))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,json_strip_nulls(v,flag) AS stripped FROM js_values ORDER BY id", []string{"id", "stripped"}, []string{"INT4", "JSON"}, [][]any{{int64(1), jsonStripDefault}, {int64(2), jsonStripArrays}}))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT v FROM js_values ORDER BY id", []string{"v"}, []string{"JSON"}, [][]any{{jsonStripInput}, {jsonStripInput}}))
	})
}

func TestReferenceJSONStripNullsNulls(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, ordinary, arrays string }{
			{`[null,null,{"a":null},[],[null,null],null]`, `[null,null,{},[],[null,null],null]`, `[{},[],[]]`},
			{`{"a":{"b":null},"d":{},"e":[null]}`, `{"a":{},"d":{},"e":[null]}`, `{"a":{},"d":{},"e":[]}`},
			{`[null]`, `[null]`, `[]`}, {`[null,1,null,2,null]`, `[null,1,null,2,null]`, `[1,2]`},
			{`null`, `null`, `null`}, {`1`, `1`, `1`}, {`true`, `true`, `true`}, {`false`, `false`, `false`}, {`"a string"`, `"a string"`, `"a string"`}, {`{}`, `{}`, `{}`}, {`[]`, `[]`, `[]`},
		} {
			for _, option := range []struct{ suffix, want string }{{"", tc.ordinary}, {",false", tc.ordinary}, {",true", tc.arrays}} {
				jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls("+jsonTypeQuote(tc.input)+option.suffix+") AS stripped", option.want))
			}
		}
		for _, args := range []string{"NULL", "NULL,false", "NULL,true", "NULL,NULL", "'{}',NULL", `'{"x":"\u0000"}',NULL`} {
			jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls("+args+") AS stripped", nil))
		}
	})
}

func TestReferenceJSONStripNullsLexical(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, want string }{
			{`{"k":1,"k":null,"k":2,"k":null}`, `{"k":1,"k":2}`},
			{`{"k":null,"k":1}`, `{"k":1}`},
			{" \n" + `{"\u0061":1.2300E+04,"a":null,"s":"\u0062\/","n":-0,"huge":1e131072}` + " \t", `{"a":1.2300E+04,"s":"b/","n":-0,"huge":1e131072}`},
			{`{"z":1,"\u0061":2,"a":3,"s":"\"\\\b\f\n\r\t","face":"\uD83D\uDE00"}`, `{"z":1,"a":2,"a":3,"s":"\"\\\b\f\n\r\t","face":"😀"}`},
		} {
			for _, flag := range []bool{false, true} {
				jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls($1,$2) AS stripped", tc.want, tc.input, flag))
			}
		}
	})
}
