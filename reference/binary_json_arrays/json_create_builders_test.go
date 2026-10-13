package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCreateBuilders(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jc_key_enum AS ENUM ('é')`, 0))
		jsonCreateCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`json_build_array()`, "JSON", `[]`}, {`jsonb_build_array()`, "JSONB", `[]`},
			{`json_build_object()`, "JSON", `{}`}, {`jsonb_build_object()`, "JSONB", `{}`},
			{`json_build_object('é'::jc_key_enum,1,'12'::oid,2)`, "JSON", `{"é" : 1, "12" : 2}`},
			{`jsonb_build_object('é'::jc_key_enum,1,'12'::oid,2)`, "JSONB", `{"12": 2, "é": 1}`},
			{`json_build_array(1,'x',TRUE,NULL,ARRAY[2,3],ROW(4,'y'))`, "JSON", `[1, "x", true, null, [2,3], {"f1":4,"f2":"y"}]`},
			{`jsonb_build_array(1,'x',TRUE,NULL,ARRAY[2,3],ROW(4,'y'))`, "JSONB", `[1, "x", true, null, [2, 3], {"f1": 4, "f2": "y"}]`},
			{`json_build_array(NULL,NULL::int,'null'::json)`, "JSON", `[null, null, null]`},
			{`jsonb_build_array(NULL,NULL::int,'null'::json)`, "JSONB", `[null, null, null]`},
			{`json_build_object('a',1,'a',2)`, "JSON", `{"a" : 1, "a" : 2}`},
			{`jsonb_build_object('a',1,'a',2)`, "JSONB", `{"a": 2}`},
			{`json_build_object(1,1,'1',2,TRUE,3,'true',4)`, "JSON", `{"1" : 1, "1" : 2, "true" : 3, "true" : 4}`},
			{`jsonb_build_object(1,1,'1',2,TRUE,3,'true',4)`, "JSONB", `{"1": 2, "true": 4}`},
			{`jsonb_build_object('aa',1,'b',2,'é',3,'z',4)`, "JSONB", `{"b": 2, "z": 4, "aa": 1, "é": 3}`},
			{`json_build_object(DATE '2020-01-02',NULL,TIMESTAMP '2020-01-02 03:04:05',true)`, "JSON", `{"2020-01-02" : null, "2020-01-02T03:04:05" : true}`},
			{`jsonb_build_object('x','{"a":1,"\u0061":2}'::json)`, "JSONB", `{"x": {"a": 2}}`},
			{`json_build_array(ARRAY[[1,4],[2,5]])`, "JSON", `[[[1,4],[2,5]]]`},
			{`jsonb_build_array(ARRAY[[1,4],[2,5]])`, "JSONB", `[[[1, 4], [2, 5]]]`},
			{`json_build_array(VARIADIC ARRAY[[1,4],[2,5]])`, "JSON", `[1, 4, 2, 5]`},
			{`jsonb_build_array(VARIADIC ARRAY[[1,4],[2,5]])`, "JSONB", `[1, 4, 2, 5]`},
			{`json_build_array(VARIADIC '[0:1][-2:-1]={{1,NULL},{3,4}}'::int[])`, "JSON", `[1, null, 3, 4]`},
			{`jsonb_build_array(VARIADIC ARRAY[1,NULL])`, "JSONB", `[1, null]`},
			{`json_build_object(VARIADIC ARRAY[['a','1'],['a','2']])`, "JSON", `{"a" : "1", "a" : "2"}`},
			{`jsonb_build_object(VARIADIC ARRAY[['a','1'],['a','2']])`, "JSONB", `{"a": "2"}`},
			{`json_build_object(VARIADIC ARRAY[1,2,3,4])`, "JSON", `{"1" : 2, "3" : 4}`},
			{`jsonb_build_object(VARIADIC ARRAY[1,2,3,4])`, "JSONB", `{"1": 2, "3": 4}`},
			{`json_build_object(VARIADIC ARRAY['a',NULL])`, "JSON", `{"a" : null}`},
			{`jsonb_build_object(VARIADIC ARRAY['a',NULL])`, "JSONB", `{"a": null}`},
			{`json_build_array(VARIADIC ARRAY[]::int[])`, "JSON", `[]`},
			{`jsonb_build_array(VARIADIC ARRAY[]::text[])`, "JSONB", `[]`},
			{`json_build_object(VARIADIC ARRAY[]::text[])`, "JSON", `{}`},
			{`jsonb_build_object(VARIADIC ARRAY[]::int[])`, "JSONB", `{}`},
			{`json_build_array(VARIADIC NULL::int[])`, "JSON", nil},
			{`jsonb_build_array(VARIADIC NULL::int[])`, "JSONB", nil},
			{`json_build_object(VARIADIC NULL::text[])`, "JSON", nil},
			{`jsonb_build_object(VARIADIC NULL::text[])`, "JSONB", nil},
			{`pg_catalog."json_build_array"('x')`, "JSON", `["x"]`},
			{`JSONB_BUILD_OBJECT('a',1)`, "JSONB", `{"a": 1}`},
		})
	})
}
