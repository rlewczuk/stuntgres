package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryValues(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{`JSON_EXISTS($1::jsonb,'$.a[*]')`, "BOOL", true},
			{`JSON_EXISTS($1::jsonb,'$.missing')`, "BOOL", false},
			{`JSON_EXISTS($1::jsonb,'$.n')`, "BOOL", true},
			{`JSON_EXISTS('false','$')`, "BOOL", true},
			{`JSON_QUERY($1::jsonb,'$.o')`, "JSONB", `{"x": 1}`},
			{`JSON_QUERY($1::jsonb,'$.s')`, "JSONB", `"123"`},
			{`JSON_QUERY($1::jsonb,'$.n')`, "JSONB", `null`},
			{`JSON_QUERY($1::jsonb,'$.missing')`, "JSONB", nil},
			{`JSON_QUERY($1::jsonb,'$.a[*]')`, "JSONB", nil},
			{`JSON_VALUE($1::jsonb,'$.s')`, "TEXT", "123"},
			{`JSON_VALUE($1::jsonb,'$.o')`, "TEXT", nil},
			{`JSON_VALUE($1::jsonb,'$.a')`, "TEXT", nil},
			{`JSON_VALUE($1::jsonb,'$.a[*]')`, "TEXT", nil},
			{`JSON_VALUE($1::jsonb,'$.missing')`, "TEXT", nil},
			{`JSON_VALUE($1::jsonb,'$.n' DEFAULT 'empty' ON EMPTY)`, "TEXT", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				args := []any{}
				if tc.expr != `JSON_EXISTS('false','$')` {
					args = append(args, sqljsonQueryDocument)
				}
				fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne("SELECT "+tc.expr+" AS v", tc.typ, tc.want, args...))
			})
		}
		for _, fn := range []struct{ name, typ string }{{"JSON_EXISTS", "BOOL"}, {"JSON_QUERY", "JSONB"}, {"JSON_VALUE", "TEXT"}} {
			for _, args := range []string{`NULL::jsonb,'$'`, `'{}',NULL::jsonpath`, `NULL::jsonb,NULL::jsonpath`} {
				t.Run(fn.name+args, func(t *testing.T) {
					q := `SELECT ` + fn.name + `(` + args + `) AS v`
					fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(q, fn.typ, nil))
					fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q+` WHERE false`, []string{"v"}, []string{fn.typ}, [][]any{}))
				})
			}
		}
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_VALUE(JSON_QUERY('{"a":{"s":"4"}}','$.a'),'$.s' RETURNING int4)`, "INT4", int64(4)},
		})
	})
}

func TestReferenceSQLJSONQueryWrappers(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_QUERY('[1,2]','$[*]' WITH WRAPPER)`, "JSONB", `[1, 2]`},
			{`JSON_QUERY('[1,2]','$[*]' WITH CONDITIONAL WRAPPER)`, "JSONB", `[1, 2]`},
			{`JSON_QUERY('[1,2]','$' WITH CONDITIONAL ARRAY WRAPPER)`, "JSONB", `[1, 2]`},
			{`JSON_QUERY('[1,2]','$' WITH UNCONDITIONAL ARRAY WRAPPER)`, "JSONB", `[[1, 2]]`},
			{`JSON_QUERY('1','$' WITH CONDITIONAL WRAPPER)`, "JSONB", `1`},
			{`JSON_QUERY('{"x":1}','$' WITH CONDITIONAL WRAPPER)`, "JSONB", `{"x": 1}`},
			{`JSON_QUERY('null','$' WITH CONDITIONAL WRAPPER)`, "JSONB", `null`},
			{`JSON_QUERY('1','$' WITH WRAPPER KEEP QUOTES)`, "JSONB", `[1]`},
			{`JSON_QUERY('{}','$.missing' WITH WRAPPER)`, "JSONB", nil},
			{`JSON_QUERY('{}','$.missing' WITH CONDITIONAL WRAPPER EMPTY ARRAY ON EMPTY)`, "JSONB", `[]`},
			{`JSON_QUERY('"123"','$' RETURNING text KEEP QUOTES ON SCALAR STRING)`, "TEXT", `"123"`},
			{`JSON_QUERY('"123"','$' RETURNING text OMIT QUOTES ON SCALAR STRING)`, "TEXT", `123`},
			{`JSON_QUERY('"123"','$' OMIT QUOTES)`, "JSONB", `123`},
			{`JSON_QUERY('"aaa"','$' OMIT QUOTES)`, "JSONB", nil},
			{`JSON_QUERY('"aaa"','$' OMIT QUOTES DEFAULT '7'::jsonb ON ERROR)`, "JSONB", `7`},
			{`JSON_QUERY('null','$' RETURNING text OMIT QUOTES)`, "TEXT", `null`},
			{`JSON_QUERY('[1,2]','$' WITHOUT ARRAY WRAPPER)`, "JSONB", `[1, 2]`},
		})
	})
}

func TestReferenceSQLJSONQueryReturning(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE DOMAIN sqjq_positive AS int CHECK(VALUE>0)`, 0))
		run(fixture.Exec(`CREATE DOMAIN sqjq_required AS int NOT NULL`, 0))
		run(fixture.Exec(`CREATE TYPE sqjq_pair AS (x int,s text)`, 0))
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_VALUE('"123"','$' RETURNING int4)`, "INT4", int64(123)},
			{`JSON_VALUE('123','$' RETURNING bigint)`, "INT8", int64(123)},
			{`JSON_VALUE('1.25','$' RETURNING numeric)`, "NUMERIC", "1.25"},
			{`JSON_VALUE('1.25','$' RETURNING int4 DEFAULT -2 ON ERROR)`, "INT4", int64(-2)},
			{`JSON_VALUE('"1.25"','$' RETURNING int4)`, "INT4", nil},
			{`JSON_VALUE('true','$')`, "TEXT", `t`},
			{`JSON_QUERY('true','$' RETURNING text)`, "TEXT", `true`},
			{`JSON_VALUE('true','$' RETURNING boolean)`, "BOOL", true},
			{`JSON_QUERY('"123"','$' RETURNING int4)`, "INT4", nil},
			{`JSON_QUERY('"123"','$' RETURNING int4 OMIT QUOTES)`, "INT4", int64(123)},
			{`JSON_VALUE('"123"','$' RETURNING json)`, "JSON", `"123"`},
			{`JSON_VALUE('"123"','$' RETURNING jsonb)`, "JSONB", `"123"`},
			{`JSON_QUERY('"abc"','$' RETURNING bytea OMIT QUOTES)`, "BYTEA", `abc`},
			{`JSON_QUERY('{"a":1}','$' RETURNING json FORMAT JSON)`, "JSON", `{"a": 1}`},
			{`JSON_QUERY('1','$' RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `1`},
			{`JSON_VALUE('"abc"','$' RETURNING varchar(3))`, "VARCHAR", `abc`},
			{`JSON_VALUE('"abcd"','$' RETURNING char(3) DEFAULT 'bad' ON ERROR)`, "BPCHAR", `bad`},
			{`JSON_QUERY('[1,2]','$' RETURNING int[])`, "_INT4", `{1,2}`},
			{`JSON_QUERY('{"x":1,"s":"a"}','$' RETURNING sqjq_pair)::text`, "TEXT", `(1,a)`},
			{`JSON_VALUE('2','$' RETURNING sqjq_positive)::int`, "INT4", int64(2)},
			{`JSON_VALUE('-1','$' RETURNING sqjq_positive DEFAULT 3 ON ERROR)::int`, "INT4", int64(3)},
			{`JSON_VALUE('2','$' RETURNING sqjq_positive)`, "INT4", int64(2)},
			{`JSON_VALUE('"2018-02-21"','$' RETURNING date)::text`, "TEXT", `2018-02-21`},
			{`JSON_VALUE('"12:34:56"','$' RETURNING time)::text`, "TEXT", `12:34:56`},
			{`JSON_VALUE('"12:34:56+10"','$' RETURNING timetz)::text`, "TEXT", `12:34:56+10`},
			{`JSON_VALUE('"2018-02-21 12:34:56"','$' RETURNING timestamp)::text`, "TEXT", `2018-02-21 12:34:56`},
		})
		t.Run("composite result", func(t *testing.T) {
			q := `SELECT JSON_QUERY('{"x":1,"s":"a"}','$' RETURNING sqjq_pair) AS v`
			for _, prepared := range []bool{false, true} {
				fixture.CompositeReferenceRows(t, p, b, c, q, [][]any{{`(1,a)`}}, prepared)
			}
		})
		for _, tc := range []struct{ expr, code, message, detail string }{
			{`JSON_VALUE('{}','$.x' RETURNING sqjq_required)`, "42804", `could not coerce ON EMPTY expression (NULL) to the RETURNING type`, `domain sqjq_required does not allow null values`},
			{`JSON_VALUE('{}','$.x' RETURNING sqjq_positive DEFAULT -1 ON EMPTY DEFAULT 9 ON ERROR)`, "42804", `could not coerce ON EMPTY expression (DEFAULT) to the RETURNING type`, `value for domain sqjq_positive violates check constraint "sqjq_positive_check"`},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+tc.expr+` AS v`, d, false)
			})
		}
	})
}
