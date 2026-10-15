package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONArguments(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY()`, "JSON", `[]`},
			{`JSON_OBJECT()`, "JSON", `{}`},
			{`JSON_ARRAY(1, TRUE, 'x', NULL, 'null' FORMAT JSON)`, "JSON", `[1, true, "x", null]`},
			{`JSON_ARRAY(1, NULL NULL ON NULL)`, "JSON", `[1, null]`},
			{`JSON_ARRAY(NULL ABSENT ON NULL)`, "JSON", `[]`},
			{`JSON_ARRAY(NULL NULL ON NULL)`, "JSON", `[null]`},
			{`JSON_ARRAY('null', 'null' FORMAT JSON)`, "JSON", `["null", null]`},
			{`JSON_OBJECT('a' VALUE 1, 'b' VALUE NULL)`, "JSON", `{"a" : 1, "b" : null}`},
			{`JSON_OBJECT('a':1, 'b':NULL NULL ON NULL)`, "JSON", `{"a" : 1, "b" : null}`},
			{`JSON_OBJECT('a':1, 'b':NULL ABSENT ON NULL)`, "JSON", `{"a" : 1}`},
			{`JSON_OBJECT('a':NULL ABSENT ON NULL)`, "JSON", `{}`},
			{`JSON_OBJECT((1 + 1) VALUE TRUE)`, "JSON", `{"2" : true}`},
			{`JSON_OBJECT(TRUE:1)`, "JSON", `{"true" : 1}`},
			{`JSON_OBJECT(FALSE:1)`, "JSON", `{"false" : 1}`},
			{`JSON_ARRAY('{"x":1}', '{"x":1}' FORMAT JSON)`, "JSON", `["{\"x\":1}", {"x":1}]`},
		})
		for _, expr := range []string{
			`JSON_OBJECT(ARRAY[1,2]:1)`,
			`JSON_OBJECT('{"a":1}'::json:1)`,
			`JSON_OBJECT('{}'::jsonb:1)`,
			`JSON_OBJECT(ROW(1,'x'):1)`,
		} {
			t.Run(expr, func(t *testing.T) {
				sqljsonDiagnostic(t, p, b, c, "SELECT "+expr+" AS v",
					fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json"), false)
			})
		}
		for _, n := range []int{100, 101, 128} {
			expr := "JSON_ARRAY(" + strings.TrimSuffix(strings.Repeat("1,", n), ",") + ")"
			want := "[" + strings.TrimSuffix(strings.Repeat("1, ", n), ", ") + "]"
			t.Run(expr, func(t *testing.T) {
				sqljsonBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"JSON"}, [][]any{{want}}))
			})
		}
	})
}

func TestReferenceSQLJSONConversion(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TYPE sqj_pair AS (n int, s text)`, 0))
		run(fixture.Exec(`CREATE DOMAIN sqj_int AS int`, 0))
		run(fixture.Exec(`CREATE DOMAIN sqj_doc AS json`, 0))
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON(' {"b":2,"a":1,"a":3} ')`, "JSON", ` {"b":2,"a":1,"a":3} `},
			{`JSON(' {"b":2,"a":1,"a":3} ' WITHOUT UNIQUE KEYS)`, "JSON", ` {"b":2,"a":1,"a":3} `},
			{`JSON(NULL::text)`, "JSON", nil},
			{`JSON('null')`, "JSON", `null`},
			{`JSON_SCALAR(1)`, "JSON", `1`},
			{`JSON_SCALAR(TRUE)`, "JSON", `true`},
			{`JSON_SCALAR('1')`, "JSON", `"1"`},
			{`JSON_SCALAR(NULL::int)`, "JSON", nil},
			{`JSON_SCALAR('{}'::json)`, "JSON", `{}`},
			{`JSON_SCALAR('{}'::jsonb)`, "JSON", `{}`},
			{`JSON_SCALAR('[1,null]'::json)`, "JSON", `[1,null]`},
			{`JSON_SCALAR(ARRAY[1,NULL,2])`, "JSON", `[1,null,2]`},
			{`JSON_SCALAR(ROW(1,'x')::sqj_pair)`, "JSON", `{"n":1,"s":"x"}`},
			{`JSON_SCALAR(2::sqj_int)`, "JSON", `2`},
			{`JSON_SCALAR('{}'::sqj_doc)`, "JSON", `{}`},
			{`JSON_SCALAR('NaN'::float8)`, "JSON", `"NaN"`},
			{`JSON_SCALAR('Infinity'::float8)`, "JSON", `"Infinity"`},
			{`JSON_SCALAR(DATE '2024-02-03')`, "JSON", `"2024-02-03"`},
			{`JSON_SCALAR(TIMESTAMP '2024-02-03 04:05:06')`, "JSON", `"2024-02-03T04:05:06"`},
			{`JSON_SERIALIZE(' {"b":2} ')`, "TEXT", ` {"b":2} `},
			{`JSON_SERIALIZE(' {"b":2,"a":1} '::json)`, "TEXT", ` {"b":2,"a":1} `},
			{`JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb::json RETURNING text)`, "TEXT", `{"a": 1, "b": 2}`},
			{`JSON_SERIALIZE(NULL::text)`, "TEXT", nil},
		})
		// Intentional divergence: native serializes JSONB as canonical JSON text.
		// Pinned PG18.0 instead returns U+0002 for this bounded two-key fixture.
		// Assert each backend independently, never compare these results for parity
		// or generalize the defect into a JSONB storage-layout contract.
		for _, expr := range []string{
			`JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb)`,
			`JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb RETURNING text)`,
			`JSON_SERIALIZE(' {"b":2,"a":1} '::jsonb FORMAT JSON RETURNING text FORMAT JSON)`,
		} {
			t.Run(expr, func(t *testing.T) {
				want := `{"a": 1, "b": 2}`
				if b.PG {
					want = "\x02"
				}
				sqljsonBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
			})
		}
	})
}

func TestReferenceSQLJSONFormat(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY('{"a":1}' FORMAT JSON)`, "JSON", `[{"a":1}]`},
			{`JSON_OBJECT('a': '[1,2]' FORMAT JSON)`, "JSON", `{"a" : [1,2]}`},
			{`JSON('{"a":1}' FORMAT JSON)`, "JSON", `{"a":1}`},
			{`JSON('\x7b2261223a317d'::bytea FORMAT JSON ENCODING UTF8)`, "JSON", `{"a":1}`},
			{`JSON_SERIALIZE('\x5b315d'::bytea FORMAT JSON ENCODING UTF8)`, "TEXT", `[1]`},
			{`JSON_ARRAY(NULL::text FORMAT JSON, 'null' FORMAT JSON NULL ON NULL)`, "JSON", `[null, null]`},
			{`JSON_ARRAY('{"b":2,"a":1}' FORMAT JSON RETURNING jsonb FORMAT JSON)`, "JSONB", `[{"a": 1, "b": 2}]`},
		})
		for _, tc := range []struct{ expr, encoding string }{
			{`JSON('\x7b002200610022003a0031007d00'::bytea FORMAT JSON ENCODING UTF16)`, "UTF16"},
			{`JSON('\x0000007b0000002200000061000000220000003a000000310000007d'::bytea FORMAT JSON ENCODING UTF32)`, "UTF32"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22023", `invalid source encoding name "`+tc.encoding+`"`)
				sqljsonDiagnostic(t, p, b, c, "SELECT "+tc.expr+" AS v", d, false)
			})
		}
	})
}

func TestReferenceSQLJSONReturning(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{`CREATE DOMAIN sqj_text AS text`, `CREATE DOMAIN sqj_json AS json`, `CREATE DOMAIN sqj_jsonb AS jsonb`, `CREATE DOMAIN sqj_varchar AS varchar(4)`, `CREATE DOMAIN sqj_positive AS text CHECK (VALUE <> '[]')`} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY('{}'::jsonb)`, "JSONB", `[{}]`},
			{`JSON_OBJECT('a':'{}'::jsonb)`, "JSONB", `{"a": {}}`},
			{`JSON_ARRAY('{}'::sqj_jsonb)`, "JSON", `[{}]`},
			{`JSON_ARRAY(1 RETURNING jsonb)`, "JSONB", `[1]`},
			{`JSON_ARRAY('{}'::jsonb RETURNING json)`, "JSON", `[{}]`},
			{`JSON_OBJECT('a':1,'a':2 RETURNING jsonb)`, "JSONB", `{"a": 2}`},
			{`JSON_OBJECT('a':1,'a':2 RETURNING jsonb FORMAT JSON)`, "JSONB", `{"a": 2}`},
			{`JSON_ARRAY(1 RETURNING text)`, "TEXT", `[1]`},
			{`JSON_ARRAY(1 RETURNING varchar(3))`, "VARCHAR", `[1]`},
			{`JSON_ARRAY(1 RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `[1]`},
			{`JSON_SERIALIZE('[1]' RETURNING bytea)`, "BYTEA", `[1]`},
			{`JSON_SERIALIZE('[1]' RETURNING varchar(3))`, "VARCHAR", `[1]`},
			{`JSON_ARRAY(1 RETURNING sqj_text)`, "TEXT", `[1]`},
			{`JSON_ARRAY(1 RETURNING sqj_json)`, "JSON", `[1]`},
			{`JSON_ARRAY(1 RETURNING sqj_jsonb)`, "JSONB", `[1]`},
			{`JSON_ARRAY(1 RETURNING sqj_varchar)`, "VARCHAR", `[1]`},
		})
		for _, q := range []string{`SELECT JSON_ARRAY(123 RETURNING varchar(3)) AS v`, `SELECT JSON_SERIALIZE('[123]' RETURNING varchar(3)) AS v`} {
			t.Run(q, func(t *testing.T) {
				sqljsonDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(3)"), false)
			})
		}
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain sqj_positive violates check constraint "sqj_positive_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "sqj_positive", "sqj_positive_check"
		t.Run("domain constraint", func(t *testing.T) {
			sqljsonDiagnostic(t, p, b, c, `SELECT JSON_ARRAY(RETURNING sqj_positive) AS v`, d, false)
		})
	})
}
