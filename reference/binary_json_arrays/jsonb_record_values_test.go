package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordValues(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c,
			`CREATE TYPE jbr_pair AS (a int,b text)`,
			`CREATE TYPE jbr_empty AS ()`,
			`CREATE TABLE jbr_row(a int NOT NULL DEFAULT 42,b text CHECK(b<>'bad'))`,
			`CREATE TYPE jbr_keys AS ("A" int,"a b" text,"é" text)`)
		for _, tc := range []struct {
			base   string
			input  any
			raw    any
			fields []any
		}{
			{"NULL", `{}`, "(,)", []any{nil, nil}},
			{"NULL", nil, nil, []any{nil, nil}},
			{"'(9,keep)'", nil, "(9,keep)", []any{int64(9), "keep"}},
			{"'(9,keep)'", `{}`, "(9,keep)", []any{int64(9), "keep"}},
			{"'(9,keep)'", `{"a":1}`, "(1,keep)", []any{int64(1), "keep"}},
			{"'(9,keep)'", `{"a":null}`, "(,keep)", []any{nil, "keep"}},
			{"NULL", `{"b":"x","a":1,"extra":{"a":"not an integer"}}`, "(1,x)", []any{int64(1), "x"}},
			{"NULL", `{"A":8,"a":1,"a":2,"b":"x"}`, "(2,x)", []any{int64(2), "x"}},
		} {
			call := "jsonb_populate_record(" + tc.base + "::jbr_pair,$1::jsonb)"
			jsonbRecordCompositeBoth(t, p, b, c, fixture.Select("SELECT "+call+" AS v", []string{"v"}, []string{""}, [][]any{{tc.raw}}, tc.input))
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT * FROM "+call, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{tc.fields}, tc.input))
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT ("+call+").a AS a", []string{"a"}, []string{"INT4"}, [][]any{{tc.fields[0]}}, tc.input))
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT ("+call+").*", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{tc.fields}, tc.input))
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record(NULL::jbr_keys,'{"A":7,"a b":"space","\u00e9":"decoded","a":99}'::jsonb)`, []string{"A", "a b", "é"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "space", "decoded"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record(NULL::jbr_row,'{"b":"bad"}'::jsonb)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "bad"}}))
		jsonbRecordCompositeBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(NULL::jbr_empty,'{}'::jsonb) AS v`, []string{"v"}, []string{""}, [][]any{{"()"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM jsonb_populate_record(NULL::jbr_empty,'{}'::jsonb)`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(NULL::jbr_pair,'{}'::jsonb)::text IS NULL AS absent,jsonb_populate_record(NULL::jbr_pair,NULL::jsonb)::text IS NULL AS whole_null`, []string{"absent", "whole_null"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM jsonb_populate_recordset(NULL::jbr_empty,'[{},{}]'::jsonb)`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT n FROM jsonb_populate_recordset(NULL::jbr_empty,'[{},{}]'::jsonb) WITH ORDINALITY AS r(n) ORDER BY n`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		jsonbRecordSetup(t, p, b, c, `ALTER TYPE jbr_pair ADD ATTRIBUTE obsolete int`, `ALTER TYPE jbr_pair DROP ATTRIBUTE obsolete`)
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM pg_catalog.jsonb_populate_record(NULL::jbr_pair,'{"obsolete":"ignored","a":4,"b":"live"}'::jsonb)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "live"}}))
	})
}

func TestReferenceJSONBRecordNested(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c,
			`CREATE TYPE jbr_inner AS (a int,b text)`,
			`CREATE TYPE jbr_outer AS (r jbr_inner,a int[],j jsonb,t text)`,
			`CREATE DOMAIN jbr_positive AS int CHECK(VALUE>0)`,
			`CREATE TYPE jbr_arrays AS (r jbr_inner[],d jbr_positive[])`,
			`CREATE TYPE jbr_limits AS (c char(2),v varchar(3),n numeric(4,1),d date,ts timestamp(2))`)
		for _, tc := range []struct {
			document string
			a, b     any
			array    string
		}{
			{`{"r":{"a":1},"a":[1,"2",null]}`, int64(1), "keep", "{1,2,NULL}"},
			{`{"r":null,"a":[]}`, nil, nil, "{}"},
			{`{"r":"(2,x)","a":"[0:1]={7,8}"}`, int64(2), "x", "[0:1]={7,8}"},
			{`{"a":[[1,2],[3,4]]}`, int64(9), "keep", "{{1,2},{3,4}}"},
		} {
			q := `SELECT (x.r).a AS a,(x.r).b AS b,x.a::text AS arr FROM jsonb_populate_record(ROW(ROW(9,'keep')::jbr_inner,ARRAY[99],NULL,NULL)::jbr_outer,$1::jsonb) x`
			jsonEachBoth(t, p, b, c, fixture.Select(q, []string{"a", "b", "arr"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{tc.a, tc.b, tc.array}}, tc.document))
		}
		for _, tc := range []struct {
			j, t         string
			wantJ, wantT any
		}{
			{`"x"`, `true`, `"x"`, "true"},
			{`{"z":2,"a":1}`, `{"z":2,"a":1}`, `{"a": 1, "z": 2}`, `{"a": 1, "z": 2}`},
			{`[1,2]`, `[1,2]`, `[1, 2]`, `[1, 2]`},
			{`12345678901234567890.1200`, `12345678901234567890.1200`, `12345678901234567890.1200`, `12345678901234567890.1200`},
			{`null`, `null`, nil, nil},
		} {
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT j,t FROM jsonb_populate_record(NULL::jbr_outer,$1::jsonb)`, []string{"j", "t"}, []string{"JSONB", "TEXT"}, [][]any{{tc.wantJ, tc.wantT}}, `{"j":`+tc.j+`,"t":`+tc.t+`}`))
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT (r[1]).a AS a,(r[2]).b AS b,d::text AS d FROM jsonb_populate_record(NULL::jbr_arrays,'{"r":[{"a":1},{"b":"x"},null],"d":[1,"2"]}'::jsonb)`, []string{"a", "b", "d"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "{1,2}"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT c::text AS c,v,n,d::text AS d,ts::text AS ts FROM jsonb_populate_record(NULL::jbr_limits,'{"c":"a","v":"abc","n":12.34,"d":"2020-01-02","ts":"2020-01-02 03:04:05.126"}'::jsonb)`, []string{"c", "v", "n", "d", "ts"}, []string{"TEXT", "VARCHAR", "NUMERIC", "TEXT", "TEXT"}, [][]any{{"a", "abc", "12.3", "2020-01-02", "2020-01-02 03:04:05.13"}}))
		jsonbRecordSetup(t, p, b, c, `SET DateStyle='ISO,DMY'`)
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT d::text AS d FROM jsonb_populate_record(NULL::jbr_limits,'{"d":"02/01/2020"}'::jsonb)`, []string{"d"}, []string{"TEXT"}, [][]any{{"2020-01-02"}}))
		jsonbRecordSetup(t, p, b, c, `CREATE DOMAIN jbr_checked_inner AS jbr_inner CHECK((VALUE).a>0)`, `CREATE TYPE jbr_deep AS (r jbr_checked_inner[],j jsonb[],a int[][])`)
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT (r[1]).a AS a,(r[2]).b AS b,j[1]::text AS object,j[2]::text AS array,a::text AS empty FROM jsonb_populate_record(NULL::jbr_deep,'{"r":[{"a":1},{"a":2,"b":"x"}],"j":[{"a":1},[1,2]],"a":[]}'::jsonb)`, []string{"a", "b", "object", "array", "empty"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "x", `{"a": 1}`, `[1, 2]`, "{}"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT j::text AS j,a::text AS a FROM jsonb_populate_record(NULL::jbr_deep,'{"j":[[1,2],[3,4]],"a":[[],[]]}'::jsonb)`, []string{"j", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"{{1,2},{3,4}}", "{}"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT j::text AS j,t FROM jsonb_populate_record(NULL::jbr_outer,'{"j":"line\nquote\"","t":"line\nquote\""}'::jsonb)`, []string{"j", "t"}, []string{"TEXT", "TEXT"}, [][]any{{`"line\nquote\""`, "line\nquote\""}}))
	})
}
