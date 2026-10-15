package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordGrammar(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, source := range []string{
			`json_to_record($1::json) AS r(a int,b text)`,
			`json_to_record($1::json) r(a int,b text)`,
			`json_to_record($1::json) AS (a int,b text)`,
			`ROWS FROM(json_to_record($1::json) AS (a int,b text))`,
			`ROWS FROM(json_to_record($1::json) AS (a int,b text)) AS r(x,y)`,
		} {
			t.Run(source, func(t *testing.T) {
				q := `SELECT * FROM ` + source
				names := []string{"a", "b"}
				if source == `ROWS FROM(json_to_record($1::json) AS (a int,b text)) AS r(x,y)` {
					names = []string{"x", "y"}
				}
				jsonRecordBoth(t, p, b, c, fixture.Select(q, names, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x"}}, `{"a":2,"b":"x"}`))
			})
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a,b,c::text AS c FROM json_to_record('{"a":2,"b":"xy","c":{"d":3}}') AS r(a integer,b character varying(2),c public.jr_sub)`, []string{"a", "b", "c"}, []string{"INT4", "VARCHAR", "TEXT"}, [][]any{{int64(2), "xy", "(3,)"}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_to_record('{"a":2}') AS (a int), json_to_record('{"b":"x","c":3}') AS (b text,c bigint))`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(2), "x", int64(3)}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT a FROM json_to_record('{"a":"x"}') AS r(a text COLLATE "C")`, []string{"a"}, []string{"TEXT"}, [][]any{{"x"}}))
		jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_pair, '{"a":2}') AS r(a,b)`, [][]any{{int64(2), nil}}))
		jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record(from_json => '{}', base => NULL::jr_pair)`, [][]any{{nil, nil}}))
		for _, tc := range []struct {
			q        string
			want     fixture.Diagnostic
			analysis bool
		}{
			{`SELECT * FROM json_to_record('{}') AS r()`, jsonRecordDiagnostic("42601", `syntax error at or near ")"`, 41), true},
			{`SELECT * FROM json_to_record('{}') AS r(a int,)`, jsonRecordDiagnostic("42601", `syntax error at or near ")"`, 47), true},
			{`SELECT * FROM json_to_record('{}') AS r(a int,b)`, jsonRecordDiagnostic("42601", `syntax error at or near ")"`, 48), true},
			{`SELECT json_populate_record(NULL::jr_pair)`, jsonRecordSignature("json_populate_record(jr_pair)", 8), true},
			{`SELECT json_to_record('{}','{}')`, jsonRecordSignature("json_to_record(unknown, unknown)", 8), true},
			{`SELECT json_populate_record(1,'{}')`, jsonRecordDiagnostic("42804", "first argument of json_populate_record must be a row type", 0), false},
			{`SELECT json_to_record(DISTINCT '{}')`, jsonRecordDiagnostic("42809", "DISTINCT specified, but json_to_record is not an aggregate function", 8), true},
			{`SELECT json_to_record('{}') OVER ()`, jsonRecordDiagnostic("42809", "OVER specified, but json_to_record is not a window function nor an aggregate function", 8), true},
			{`SELECT json_populate_record(VARIADIC ARRAY['{}'])`, jsonRecordSignature("json_populate_record(text[])", 8), true},
			{`SELECT * FROM json_to_record(bad_name => '{}') AS r(a int)`, jsonRecordSignature("json_to_record(bad_name => unknown)", 15), true},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, tc.analysis) })
		}
	})
}

func TestReferenceJSONRecordMetadata(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q     string
			input any
			rows  [][]any
		}{
			{`SELECT * FROM json_to_record($1::json) AS r(a int,b text)`, `{"a":2}`, [][]any{{int64(2), nil}}},
			{`SELECT * FROM json_to_record($1::json) AS r(a int,b text)`, nil, [][]any{{nil, nil}}},
			{`SELECT * FROM json_to_recordset($1::json) AS r(a int,b text)`, `[]`, nil},
			{`SELECT * FROM json_to_recordset($1::json) AS r(a int,b text)`, nil, nil},
			{`SELECT * FROM json_populate_record(NULL::jr_pair,$1::json)`, `{"a":2}`, [][]any{{int64(2), nil}}},
			{`SELECT * FROM json_populate_record(NULL::jr_pair,$1::json)`, nil, [][]any{{nil, nil}}},
			{`SELECT * FROM json_populate_recordset(NULL::jr_pair,$1::json)`, `[]`, nil},
		} {
			t.Run(tc.q+"/"+metadataInputName(tc.input), func(t *testing.T) {
				jsonRecordBoth(t, p, b, c, jsonRecordMetadata(jsonRecordPair(tc.q, tc.rows, tc.input), b.PG))
			})
		}
		for _, step := range []fixture.Step{
			fixture.Select(`SELECT json_populate_record(NULL::jr_pair,'{"a":2}') AS r`, []string{"r"}, []string{""}, [][]any{{"(2,)"}}),
			fixture.Select(`SELECT json_populate_record(NULL::jr_pair,NULL::json) AS r`, []string{"r"}, []string{""}, [][]any{{nil}}),
			fixture.Select(`SELECT json_populate_recordset(NULL::jr_pair,'[]') AS r`, []string{"r"}, []string{""}, nil),
			fixture.Select(`SELECT * FROM json_to_record('{}') AS r(a varchar(3),b numeric(5,2),c jr_sub,d int[],e json,f jsonb)`, []string{"a", "b", "c", "d", "e", "f"}, []string{"VARCHAR", "NUMERIC", "", "_INT4", "JSON", "JSONB"}, [][]any{{nil, nil, nil, nil, nil, nil}}),
			fixture.Select(`SELECT * FROM ROWS FROM(json_to_recordset('[]') AS (a int,b text)) WITH ORDINALITY AS r(a,b,n)`, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, nil),
			fixture.Select(`SELECT * FROM json_populate_record(NULL::jr_empty,'{}')`, []string{}, []string{}, [][]any{{}}),
		} {
			t.Run(step.Query, func(t *testing.T) { jsonRecordBoth(t, p, b, c, jsonRecordMetadata(step, b.PG)) })
		}
	})
}

func metadataInputName(input any) string {
	if input == nil {
		return "SQL-NULL"
	}
	return input.(string)
}
