package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordValues(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, input    string
			base, nullBase [][]any
		}{
			{"missing", `{"a":"2","ignored":99}`, [][]any{{int64(2), "keep"}}, [][]any{{int64(2), nil}}},
			{"explicit-null", `{"b":null}`, [][]any{{int64(7), nil}}, [][]any{{nil, nil}}},
			{"empty", `{}`, [][]any{{int64(7), "keep"}}, [][]any{{nil, nil}}},
			{"decoded-key", `{"\u0061":2}`, [][]any{{int64(2), "keep"}}, [][]any{{int64(2), nil}}},
			{"case-sensitive", `{"A":2}`, [][]any{{int64(7), "keep"}}, [][]any{{nil, nil}}},
			{"last-duplicate", `{"a":"bad","a":2}`, [][]any{{int64(2), "keep"}}, [][]any{{int64(2), nil}}},
			{"long-key", `{"` + strings.Repeat("a", 64) + `":2}`, [][]any{{int64(7), "keep"}}, [][]any{{nil, nil}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record('(7,keep)'::jr_pair, $1::json)`, tc.base, tc.input))
				jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_pair, $1::json)`, tc.nullBase, tc.input))
				jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_to_record($1::json) AS r(a int,b text)`, tc.nullBase, tc.input))
			})
		}
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{`SELECT * FROM json_populate_record('(7,keep)'::jr_pair, NULL::json)`, [][]any{{int64(7), "keep"}}},
			{`SELECT * FROM json_populate_record(NULL::jr_pair, NULL::json)`, [][]any{{nil, nil}}},
			{`SELECT * FROM json_to_record(NULL::json) AS r(a int,b text)`, [][]any{{nil, nil}}},
			{`SELECT * FROM json_populate_recordset(NULL::jr_pair, NULL::json)`, nil},
			{`SELECT * FROM json_to_recordset(NULL::json) AS r(a int,b text)`, nil},
			{`SELECT * FROM json_populate_recordset(NULL::jr_pair, '[]')`, nil},
			{`SELECT * FROM json_to_recordset('[]') AS r(a int,b text)`, nil},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonRecordBoth(t, p, b, c, jsonRecordPair(tc.q, tc.rows)) })
		}
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{`SELECT a,b,n FROM json_populate_recordset('(7,keep)'::jr_pair, $1::json) WITH ORDINALITY AS r(a,b,n) ORDER BY n`, [][]any{{int64(2), "keep", int64(1)}, {int64(7), "x", int64(2)}, {int64(7), "keep", int64(3)}}},
			{`SELECT a,b,n FROM ROWS FROM(json_to_recordset($1::json) AS (a int,b text)) WITH ORDINALITY AS r(a,b,n) ORDER BY n`, [][]any{{int64(2), nil, int64(1)}, {nil, "x", int64(2)}, {nil, nil, int64(3)}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				jsonRecordBoth(t, p, b, c, fixture.Select(tc.q, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, tc.rows, `[{"a":2},{"b":"x"},{}]`))
			})
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT json_populate_record(NULL::jr_pair, '{"a":2}')::text AS r`, []string{"r"}, []string{"TEXT"}, [][]any{{"(2,)"}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT json_populate_record(NULL::jr_pair, NULL::json)::text AS r`, []string{"r"}, []string{"TEXT"}, [][]any{{nil}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT json_populate_recordset(NULL::jr_pair, '[{"a":2},{"b":"x"}]')::text AS r ORDER BY r`, []string{"r"}, []string{"TEXT"}, [][]any{{"(,x)"}, {"(2,)"}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM json_to_record('{"A":2,"a":3}') AS r("A" int,a int)`, []string{"A", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT j::text AS j, k::text AS k, t FROM json_to_record($1::json) AS r(j json,k jsonb,t text)`, []string{"j", "k", "t"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, `"null"`, `[1, 2]`}}, `{"j":null,"k":"null","t":[1, 2]}`))
	})
}

func TestReferenceJSONRecordNested(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		input := `{"a":1,"b":[2,"a b"],"c":{"d":4,"e":"a b c"}}`
		for _, source := range []string{
			`json_populate_record(NULL::jr_outer, $1::json)`,
			`json_to_record($1::json) AS r(a int,b text[],c jr_sub)`,
		} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a,b::text AS b,c::text AS c FROM `+source, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), `{2,"a b"}`, `(4,"a b c")`}}, input))
		}
		for _, tc := range []struct{ input, nested any }{
			{`{"c":{"d":9}}`, "(9,keep)"},
			{`{"c":null}`, nil},
			{`{}`, "(4,keep)"},
		} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT c::text AS c FROM json_populate_record(ROW(1,ARRAY['old'],ROW(4,'keep')::jr_sub)::jr_outer, $1::json)`, []string{"c"}, []string{"TEXT"}, [][]any{{tc.nested}}, tc.input))
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT c::text AS c,n FROM json_populate_recordset(ROW(1,ARRAY['old'],ROW(4,'keep')::jr_sub)::jr_outer, '[{"c":{"d":9}},{"c":{"e":"new"}}]') WITH ORDINALITY AS r(a,b,c,n) ORDER BY n`, []string{"c", "n"}, []string{"TEXT", "INT8"}, [][]any{{"(9,keep)", int64(1)}, {"(4,new)", int64(2)}}))
		for _, tc := range []struct{ input, value string }{
			{`{"a":[[1,2],[3,4]]}`, "{{1,2},{3,4}}"},
			{`{"a":[1,null,3]}`, "{1,NULL,3}"},
			{`{"a":[]}`, "{}"},
			{`{"a":"{1,2}"}`, "{1,2}"},
			{`{"a":"[0:1]={1,2}"}`, "[0:1]={1,2}"},
		} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a::text AS a FROM json_to_record($1::json) AS r(a int[])`, []string{"a"}, []string{"TEXT"}, [][]any{{tc.value}}, tc.input))
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT array_dims(a) AS dims,array_lower(a,1) AS lo FROM json_to_record('{"a":[[1,2],[3,4]]}') AS r(a int[])`, []string{"dims", "lo"}, []string{"TEXT", "INT4"}, [][]any{{"[1:2][1:2]", int64(1)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT (a[1]).d AS d,(a[1]).e AS e,a[2]::text AS absent,(a[3]).d AS last FROM json_to_record('{"a":[{"d":4,"e":"x"},null,{"d":9}]}') AS r(a jr_sub[])`, []string{"d", "e", "absent", "last"}, []string{"INT4", "TEXT", "TEXT", "INT4"}, [][]any{{int64(4), "x", nil, int64(9)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a::text AS a FROM json_to_record('{"a":"(4,x)"}') AS r(a jr_sub)`, []string{"a"}, []string{"TEXT"}, [][]any{{"(4,x)"}}))
		// Inspect the typed NUMERIC result directly: native NUMERIC-to-text
		// explicit coercion is a separate capability, not this conversion.
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a,b FROM json_to_record('{"a":"ab","b":12.34}') AS r(a varchar(2),b numeric(4,1))`, []string{"a", "b"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"ab", "12.3"}}))
		if b.PG {
			// Retain the independently observed PostgreSQL explicit-cast route.
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a,b::text AS b FROM json_to_record('{"a":"ab","b":12.34}') AS r(a varchar(2),b numeric(4,1))`, []string{"a", "b"}, []string{"VARCHAR", "TEXT"}, [][]any{{"ab", "12.3"}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jr_array_base AS (a integer[])`, 0))
		for _, tc := range []struct{ input, dimensions string }{
			{`{"a":[1,2]}`, "[1:2]"},
			{`{}`, "[0:1]"},
		} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT array_dims(a) AS dims FROM json_populate_record(ROW('[0:1]={8,9}'::int[])::jr_array_base,$1::json)`, []string{"dims"}, []string{"TEXT"}, [][]any{{tc.dimensions}}, tc.input))
		}
		for _, input := range []string{`{"a":[[],[]]}`, `{"a":[[[]]]}`} {
			jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a::text,array_dims(a) FROM json_to_record($1::json) AS r(a int[])`, []string{"a", "array_dims"}, []string{"TEXT", "TEXT"}, [][]any{{"{}", nil}}, input))
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a::text FROM json_to_record($1::json) AS r(a json)`, []string{"a"}, []string{"TEXT"}, [][]any{{"\"a\\né\""}}, `{"a":"\u0061\n\u00e9"}`))
	})
}
