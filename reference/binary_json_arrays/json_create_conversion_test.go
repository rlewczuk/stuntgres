package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCreateConversion(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		for _, q := range []string{
			`CREATE TYPE jc_pair AS (n int,t text)`,
			`CREATE DOMAIN jc_pair_domain AS jc_pair`,
			`CREATE DOMAIN jc_number AS numeric(8,3)`,
			`CREATE DOMAIN jc_ints AS int[]`,
			`CREATE TYPE jc_enum AS ENUM ('é','two')`,
			`CREATE TYPE jc_nested AS (pair jc_pair,nums int[],doc json,canon jsonb,d jc_number)`,
		} {
			run(q)
		}
		for _, tc := range []struct{ input, json, jsonb string }{
			{`42`, `42`, `42`}, {`TRUE`, `true`, `true`}, {`12.340::numeric`, `12.340`, `12.340`},
			{`'x'::text`, `"x"`, `"x"`}, {`'12'::oid`, `"12"`, `"12"`}, {`'x'::char(3)`, `"x  "`, `"x  "`},
			{`'é'::jc_enum`, `"é"`, `"é"`}, {`12.340::jc_number`, `12.340`, `12.340`},
			{`'nm'::name`, `"nm"`, `"nm"`}, {`'00000000-0000-0000-0000-000000000001'::uuid`, `"00000000-0000-0000-0000-000000000001"`, `"00000000-0000-0000-0000-000000000001"`},
			{`'\x00ff'::bytea`, `"\\x00ff"`, `"\\x00ff"`},
			{`B'101'`, `"101"`, `"101"`},
			{`'192.0.2.1/24'::inet`, `"192.0.2.1/24"`, `"192.0.2.1/24"`}, {`'192.0.2.0/24'::cidr`, `"192.0.2.0/24"`, `"192.0.2.0/24"`},
			{`'$'::jsonpath`, `"$"`, `"$"`}, {`'text'::regtype`, `"text"`, `"text"`},
			{`1::int2`, `1`, `1`}, {`9223372036854775807::int8`, `9223372036854775807`, `9223372036854775807`},
			{`1.25::float4`, `1.25`, `1.25`}, {`1.25::float8`, `1.25`, `1.25`},
			{`'x'::varchar(3)`, `"x"`, `"x"`}, {`false`, `false`, `false`},
			{`'null'::json`, `null`, `null`}, {`'{"a":1,"a":2}'::jsonb`, `{"a": 2}`, `{"a": 2}`},
			{`ARRAY[1.230::jc_number,NULL::jc_number]`, `[1.230,null]`, `[1.230, null]`},
			{`ROW(1,'x')::jc_pair_domain`, `{"n":1,"t":"x"}`, `{"n": 1, "t": "x"}`},
			{`'NaN'::float8`, `"NaN"`, `"NaN"`}, {`'Infinity'::float4`, `"Infinity"`, `"Infinity"`}, {`'-Infinity'::numeric`, `"-Infinity"`, `"-Infinity"`},
			{`ARRAY[[1,NULL],[3,4]]`, `[[1,null],[3,4]]`, `[[1, null], [3, 4]]`},
			{`'[0:1][-2:-1]={{1,NULL},{3,4}}'::int[]`, `[[1,null],[3,4]]`, `[[1, null], [3, 4]]`},
			{`ARRAY[1,NULL]::jc_ints`, `[1,null]`, `[1, null]`},
			{`ARRAY[ARRAY[1,2]::jc_ints,ARRAY[3,NULL]::jc_ints]`, `[[1,2],[3,null]]`, `[[1, 2], [3, null]]`},
			{`ROW(1,'x',NULL::int)`, `{"f1":1,"f2":"x","f3":null}`, `{"f1": 1, "f2": "x", "f3": null}`},
			{`ROW(NULL::int,NULL::text)`, `{"f1":null,"f2":null}`, `{"f1": null, "f2": null}`},
			{`ROW(1,'x')::jc_pair`, `{"n":1,"t":"x"}`, `{"n": 1, "t": "x"}`},
			{`ROW(ROW(1,'x')::jc_pair,ARRAY[2,NULL],'{ "a":1,"a":2 }'::json,'{"aa":1,"b":2}'::jsonb,12.340)::jc_nested`, `{"pair":{"n":1,"t":"x"},"nums":[2,null],"doc":{ "a":1,"a":2 },"canon":{"b": 2, "aa": 1},"d":12.340}`, `{"d": 12.340, "doc": {"a": 2}, "nums": [2, null], "pair": {"n": 1, "t": "x"}, "canon": {"b": 2, "aa": 1}}`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT to_json("+tc.input+") AS v", "JSON", tc.json))
				jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT to_jsonb("+tc.input+") AS v", "JSONB", tc.jsonb))
			})
		}
		run(`CREATE TABLE jc_carriers(v varbit,r int4range,m int4multirange)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jc_carriers VALUES('101','[1,3)','{[1,3),[5,7)}')`, 1))
		for _, tc := range []struct{ column, want string }{
			{"v", `"101"`}, {"r", `"[1,3)"`}, {"m", `"{[1,3),[5,7)}"`},
		} {
			t.Run(tc.column+" typed carrier", func(t *testing.T) {
				jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT to_json("+tc.column+") AS v FROM jc_carriers", "JSON", tc.want))
				jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT to_jsonb("+tc.column+") AS v FROM jc_carriers", "JSONB", tc.want))
			})
		}
		jsonCreateCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`to_json(NULL::int)`, "JSON", nil}, {`to_jsonb(NULL::jc_pair)`, "JSONB", nil}, {`to_json(NULL::jc_pair)`, "JSON", nil},
			{`array_to_json(ARRAY[[1,2],[3,4]])`, "JSON", `[[1,2],[3,4]]`},
			{`array_to_json(ARRAY[[1,2],[3,4]],true)`, "JSON", "[[1,2],\n [3,4]]"},
			{`array_to_json(ARRAY[1,NULL],false)`, "JSON", `[1,null]`},
			{`array_to_json(NULL::int[])`, "JSON", nil}, {`array_to_json(ARRAY[1],NULL::bool)`, "JSON", nil},
			{`row_to_json(ROW(1,ROW(2,3)),true)`, "JSON", "{\"f1\":1,\n \"f2\":{\"f1\":2,\"f2\":3}}"},
			{`row_to_json(ROW(1,'x'),false)`, "JSON", `{"f1":1,"f2":"x"}`},
			{`row_to_json(NULL::jc_pair)`, "JSON", nil}, {`row_to_json(ROW(1),NULL::bool)`, "JSON", nil},
			{`pg_catalog.to_json(VARIADIC ARRAY[1,2])`, "JSON", `[1,2]`},
			{`pg_catalog.array_to_json(VARIADIC ARRAY[1,2])`, "JSON", `[1,2]`},
			{`pg_catalog.to_jsonb(VARIADIC ARRAY[1,2])`, "JSONB", `[1, 2]`},
			{`pg_catalog.row_to_json(VARIADIC ROW(1,'x'))`, "JSON", `{"f1":1,"f2":"x"}`},
			{`array_to_json(ARRAY[1,2],'true')`, "JSON", "[1,\n 2]"},
		})
		run(`CREATE TABLE jc_presence(id int,n int,t text)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jc_presence VALUES(1,NULL,NULL)`, 1))
		jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT x.id,to_json(r) AS j,to_jsonb(r) AS jb FROM (VALUES(1),(2)) AS x(id) LEFT JOIN jc_presence r ON r.id=x.id ORDER BY x.id`, []string{"id", "j", "jb"}, []string{"INT4", "JSON", "JSONB"}, [][]any{{int64(1), `{"id":1,"n":null,"t":null}`, `{"n": null, "t": null, "id": 1}`}, {int64(2), nil, nil}}))
		run(`ALTER TYPE jc_pair RENAME ATTRIBUTE t TO "é""key"`)
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT row_to_json(ROW(1,'x')::jc_pair) AS v`, "JSON", `{"n":1,"é\"key":"x"}`))
		run(`ALTER TYPE jc_pair DROP ATTRIBUTE n`)
		run(`ALTER TYPE jc_pair ADD ATTRIBUTE z int`)
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT row_to_json(ROW('x',2)::jc_pair) AS v`, "JSON", `{"é\"key":"x","z":2}`))
	})
}

func TestReferenceJSONCreateEscaping(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonCreateCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`to_json(E'"\\\b\f\n\r\t\001é😀<>&/'::text)`, "JSON", `"\"\\\b\f\n\r\t\u0001é😀<>&/"`},
			{`to_jsonb(E'"\\\b\f\n\r\t\001é😀<>&/'::text)`, "JSONB", `"\"\\\b\f\n\r\t\u0001é😀<>&/"`},
			{`json_build_object(E'"\\',E'\b\f\n\r\t\001','é😀','<>&/')`, "JSON", `{"\"\\" : "\b\f\n\r\t\u0001", "é😀" : "<>&/"}`},
			{`to_json('{ "a":1,"\u0061":2 }'::json)`, "JSON", `{ "a":1,"\u0061":2 }`},
			{`to_jsonb('{ "a":1,"\u0061":2 }'::json)`, "JSONB", `{"a": 2}`},
			{`json_build_array('{ "a":1,"a":2 }'::json,'{"a":1,"a":2}'::jsonb)`, "JSON", `[{ "a":1,"a":2 }, {"a": 2}]`},
			{`to_json(' '::text)`, "JSON", "\" \""},
		})
	})
}
