package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonpathMethodsA = `[null,1,true,"11",[],[1],[1,2,3],{}, {"a":1,"b":2}]`
const jsonpathMethodsB = `[true,false,0,1,-2,"yes","NO","of","ON"]`
const jsonpathMethodsC = `[1.2300,"xyz",false]`
const jsonpathMethodsO = `{"x":"20","y":32,"z":null}`
const jsonpathMethodsSizeError = `jsonpath item method .size() can only be applied to an array`
const jsonpathMethodsBooleanError = `jsonpath item method .boolean() can only be applied to a boolean, string, or numeric value`
const jsonpathMethodsStringError = `jsonpath item method .string() can only be applied to a boolean, string, numeric, or datetime value`
const jsonpathMethodsKeyValueError = `jsonpath item method .keyvalue() can only be applied to an object`

func TestReferenceJSONPathMethodsShape(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{jsonpathMethodsA, `$[*].type()`, ``, `["null", "number", "boolean", "string", "array", "array", "array", "object", "object"]`},
		{jsonpathMethodsA, `$.type()`, ``, `["array"]`},
		{jsonpathMethodsA, `strict $.type()`, ``, `["array"]`},
		{jsonpathMethodsA, `$.size()`, ``, `[9]`},
		{jsonpathMethodsA, `$[*].size()`, ``, `[1, 1, 1, 1, 0, 1, 3, 1, 1]`},
		{`7`, `$.size()`, ``, `[1]`}, {`[]`, `strict $.size()`, ``, `[0]`},
		{`null`, `$.type()`, ``, `["null"]`}, {`{}`, `$.absent.type().size()`, ``, `[]`},
		{`{"type":1}`, `$.type`, ``, `[1]`},
		{`{"size":2}`, `$.size`, ``, `[2]`},
		{`{"boolean":3}`, `$.boolean`, ``, `[3]`},
		{`{"string":4}`, `$.string`, ``, `[4]`},
		{`{"keyvalue":5}`, `$.keyvalue`, ``, `[5]`},
		{`[[],[1,2]]`, `strict $[*].size()`, ``, `[0, 2]`},
	})
}

func TestReferenceJSONPathMethodsConversions(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{jsonpathMethodsB, `$[*].boolean()`, ``, `[true, false, false, true, true, true, false, false, true]`},
		{jsonpathMethodsB, `strict $[*].boolean()`, ``, `[true, false, false, true, true, true, false, false, true]`},
		{jsonpathMethodsC, `$[*].string()`, ``, `["1.2300", "xyz", "false"]`},
		{`[true,false,9007199254740993,-0.00,1e2,1.00e2]`, `$[*].string()`, ``, `["true", "false", "9007199254740993", "0.00", "100", "100"]`},
		{`[0,-2,-2147483648,2147483647,1e2,1.00e2]`, `$[*].boolean()`, ``, `[false, true, true, true, true, true]`},
		{`["t","TR","y","Ye","n","f","fa","of","ON","1","0"]`, `$[*].boolean()`, ``, `[true, true, true, true, false, false, false, false, true, true, false]`},
		{`["a\"b","line\nnext","é雪"]`, `$[*].string()`, ``, `["a\"b", "line\nnext", "é雪"]`},
		{`["yes",0]`, `$[*].boolean().string().type()`, ``, `["string", "string"]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1::jsonb,'$.boolean()') AS changed`
		for _, tc := range []struct{ doc, rendered string }{
			{`1.0`, `1.0`}, {`-0.0`, `0.0`}, {`1.5`, `1.5`}, {`2147483648`, `2147483648`}, {`-2147483649`, `-2147483649`},
			{`"100"`, `100`}, {`"o"`, `o`}, {`""`, ``}, {`" yes"`, ` yes`}, {`"true "`, `true `}, {`"é"`, `é`}, {`"a\"b\\雪"`, `a"b\雪`},
		} {
			t.Run(tc.doc, func(t *testing.T) {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22036", fmt.Sprintf(`argument "%s" of jsonpath item method .boolean() is invalid for type boolean`, tc.rendered), tc.doc))
			})
		}
	})
}

func TestReferenceJSONPathMethodsSuppression(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[]`, `$.boolean()`, ``, `[]`}, {`[]`, `$.string()`, ``, `[]`}, {`[]`, `$.keyvalue()`, ``, `[]`},
		{`null`, `exists($.boolean())`, ``, `[null]`}, {`null`, `($.boolean()==true) is unknown`, ``, `[true]`},
		{`[null,1,0]`, `$[*]?(@.boolean()==true)`, ``, `[1]`},
		{`{}`, `$.missing.boolean()`, ``, `[]`},
		{`[1,[2]]`, `strict $.**.size()`, ``, `[2, 1]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1,$2,'{}',$3) AS changed`
		for _, tc := range []struct{ doc, path, code, message, prefix string }{
			{`[1,"bad",0]`, `$[*].boolean()`, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`, `[true]`},
			{`[1,null,2]`, `$[*].string()`, "22036", jsonpathMethodsStringError, `["1"]`},
			{`[{"a":1},0,{"b":2}]`, `$[*].keyvalue().key`, "2203C", jsonpathMethodsKeyValueError, `["a"]`},
			{jsonpathMethodsA, `strict $[*].size()`, "22039", jsonpathMethodsSizeError, `[]`},
			{`7`, `strict $.size()`, "22039", jsonpathMethodsSizeError, `[]`},
			{`null`, `$.boolean()`, "22036", jsonpathMethodsBooleanError, `[]`},
			{`{}`, `strict $.missing.boolean()`, "2203A", `JSON object does not contain key "missing"`, `[]`},
			{`{"a":null}`, `strict $.**.boolean()`, "22036", jsonpathMethodsBooleanError, `[]`},
		} {
			t.Run(tc.path+tc.doc, func(t *testing.T) {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, tc.code, tc.message, tc.doc, tc.path, false))
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, tc.prefix, tc.doc, tc.path, true))
			})
		}
		for _, method := range []struct{ name, code, message string }{{"boolean", "22036", jsonpathMethodsBooleanError}, {"string", "22036", jsonpathMethodsStringError}, {"keyvalue", "2203C", jsonpathMethodsKeyValueError}} {
			for _, tc := range []struct{ doc, mode string }{{`[[]]`, ``}, {`[]`, `strict `}, {`[1]`, `strict `}} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, method.code, method.message, tc.doc, tc.mode+`$.`+method.name+`()`, false))
			}
		}
		for _, silent := range []bool{false, true} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `1`, `$v.boolean()`, silent))
		}
	})
}
