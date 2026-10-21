package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryPassing(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
		run(fixture.Exec(`SET DateStyle TO 'ISO, YMD'`, 0))
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_EXISTS('[1,"1"]','$[*] ? (@ == $x)' PASSING 1 AS x)`, "BOOL", true},
			{`JSON_QUERY('[1,"1"]','$[*] ? (@ == $x)' PASSING '1'::text AS x)`, "JSONB", `"1"`},
			{`JSON_QUERY('[1,"1"]','$[*] ? (@ == $x)' PASSING 1 AS x)`, "JSONB", `1`},
			{`JSON_QUERY('{}','$x' PASSING NULL::int AS x)`, "JSONB", `null`},
			{`JSON_EXISTS('{}','$x' PASSING NULL::date AS x)`, "BOOL", true},
			{`JSON_VALUE('{}','$x' PASSING NULL::date AS x RETURNING date DEFAULT '2000-01-01' ON EMPTY)::text`, "TEXT", nil},
			{`JSON_QUERY('{}','$x' PASSING '[1,{"a":2}]'::json AS x)`, "JSONB", `[1, {"a": 2}]`},
			{`JSON_QUERY('{}','$x.a' PASSING '{"a":2}'::jsonb AS x)`, "JSONB", `2`},
			{`JSON_QUERY('{}','$x' PASSING ARRAY[1,2] AS x)`, "JSONB", `[1, 2]`},
			{`JSON_QUERY('{}','$x' PASSING ROW(1,'a'::text) AS x)`, "JSONB", `{"f1": 1, "f2": "a"}`},
			{`JSON_QUERY('{}','$x' PASSING true AS x)`, "JSONB", `true`},
			{`JSON_VALUE('{}','$X == $x' PASSING 1 AS "X", 2 AS x RETURNING boolean)`, "BOOL", false},
			{`JSON_VALUE('{}','$x' PASSING 1 AS x,2 AS x RETURNING int4)`, "INT4", int64(1)},
		})
		for _, tc := range []struct{ sql, text, json, typ string }{
			{`DATE '2018-02-21'`, `2018-02-21`, `"2018-02-21"`, "DATE"},
			{`TIME '12:34:56'`, `12:34:56`, `"12:34:56"`, "TIME"},
			{`TIMETZ '12:34:56+10'`, `12:34:56+10`, `"12:34:56+10:00"`, "TIMETZ"},
			{`TIMESTAMP '2018-02-21 12:34:56'`, `2018-02-21 12:34:56`, `"2018-02-21T12:34:56"`, "TIMESTAMP"},
			{`TIMESTAMPTZ '2018-02-21 12:34:56+10'`, `2018-02-21 02:34:56+00`, `"2018-02-21T02:34:56+00:00"`, "TIMESTAMPTZ"},
		} {
			t.Run(tc.typ, func(t *testing.T) {
				passing := ` PASSING ` + tc.sql + ` AS v`
				sqljsonQueryCases(t, p, b, c, []struct {
					expr, typ string
					want      any
				}{
					{`JSON_EXISTS('{}','$v'` + passing + `)`, "BOOL", true},
					{`JSON_VALUE('{}','$v'` + passing + `)`, "TEXT", tc.text},
					{`JSON_QUERY('{}','$v'` + passing + `)`, "JSONB", tc.json},
					{`JSON_VALUE('{}','$v'` + passing + ` RETURNING json)`, "JSON", tc.json},
					{`JSON_VALUE('{}','$v'` + passing + ` RETURNING jsonb)`, "JSONB", tc.json},
					{`JSON_VALUE('{}','$v'` + passing + ` RETURNING ` + tc.typ + `)::text`, "TEXT", tc.text},
					{`JSON_QUERY('{}','$v'` + passing + ` WITH CONDITIONAL WRAPPER)`, "JSONB", tc.json},
					{`JSON_QUERY('{}','$v'` + passing + ` WITH UNCONDITIONAL WRAPPER)`, "JSONB", `[` + tc.json + `]`},
					{`JSON_EXISTS('{}','$v' PASSING NULL::` + tc.typ + ` AS v)`, "BOOL", true},
					{`JSON_QUERY('{}','$v' PASSING NULL::` + tc.typ + ` AS v)`, "JSONB", `null`},
					{`JSON_VALUE('{}','$v' PASSING NULL::` + tc.typ + ` AS v RETURNING ` + tc.typ + `)::text`, "TEXT", nil},
				})
				// The typed destination is checked on RowDescription even with no row.
				q := `SELECT JSON_VALUE('{}','$v'` + passing + ` RETURNING ` + tc.typ + `) AS v WHERE false`
				sqljsonBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{}))
			})
		}
		for _, tc := range []struct {
			left, right string
			want        any
		}{
			{`DATE '2018-02-21'`, `DATE '2018-02-21'`, true},
			{`DATE '2018-02-21'`, `TIMESTAMP '2018-02-21 00:00:00'`, true},
			{`DATE '2018-02-21'`, `TIMESTAMPTZ '2018-02-21 00:00:00+00'`, true},
			{`TIMESTAMP '2018-02-21 00:00:00'`, `TIMESTAMPTZ '2018-02-21 00:00:00+00'`, true},
			{`TIME '12:00'`, `TIME '12:00'`, true},
			{`TIME '12:00'`, `TIMETZ '12:00+00'`, true},
			{`TIMETZ '12:00+00'`, `TIMETZ '13:00+01'`, false},
			{`TIMESTAMP '2018-02-21'`, `TIMESTAMP '2018-02-21'`, true},
			{`TIMESTAMPTZ '2018-02-21+00'`, `TIMESTAMPTZ '2018-02-21+00'`, true},
			{`DATE '2018-02-21'`, `'2018-02-21'::text`, nil},
			{`DATE '2018-02-21'`, `TIME '00:00'`, nil},
			{`DATE '2018-02-21'`, `1`, nil},
			{`NULL::date`, `NULL::timestamptz`, true},
		} {
			t.Run(tc.left+"="+tc.right, func(t *testing.T) {
				q := `SELECT JSON_VALUE('{}','$a == $b' PASSING ` + tc.left + ` AS a,` + tc.right + ` AS b RETURNING boolean ERROR ON ERROR) AS v`
				sqljsonBoth(t, p, b, c, sqljsonQueryOne(q, "BOOL", tc.want))
			})
		}
		run(fixture.Exec(`CREATE SEQUENCE sqjq_order`, 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				doc, path string
				want      any
				last      int64
				called    bool
			}{
				{`'{}'`, `'$x'`, int64(1), 3, true},
				{`NULL::jsonb`, `'$x'`, nil, 1, false},
				{`'{}'`, `NULL::jsonpath`, nil, 1, false},
			} {
				run(sqljsonQueryOne(`SELECT setval('sqjq_order',1,false) AS v`, "INT8", int64(1)))
				q := `SELECT JSON_VALUE(` + tc.doc + `,` + tc.path + ` PASSING nextval('sqjq_order') AS x,nextval('sqjq_order') AS x,nextval('sqjq_order') AS unused RETURNING bigint) AS v`
				s := sqljsonQueryOne(q, "INT8", tc.want)
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT last_value,is_called FROM sqjq_order`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{tc.last, tc.called}}))
			}
		}
	})
}
