package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Values are independent manual/source-backed expectations, not backend snapshots.
func stringVariadicValues(t *testing.T, cases []struct {
	call string
	want any
}) {
	t.Helper()
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.call, func(t *testing.T) {
				formatReferenceBoth(t, p, b, c, fixture.Select("SELECT "+tc.call+" AS value", []string{"value"}, []string{"TEXT"}, [][]any{{tc.want}}))
			})
		}
	})
}

func TestReferenceStringVariadicValues(t *testing.T) {
	stringVariadicValues(t, []struct {
		call string
		want any
	}{
		{`concat('abcde',2,NULL,22)`, "abcde222"}, {`concat(NULL,NULL)`, ""}, {`concat('',NULL,'')`, ""},
		{`concat_ws(',', 'abcde',2,NULL,22)`, "abcde,2,22"}, {`concat_ws(',',NULL,NULL)`, ""},
		{`concat_ws(',', '',NULL,'x','')`, ",x,"}, {`concat_ws('', 'a','b')`, "ab"}, {`concat_ws(NULL,'a','b')`, nil},
	})
}

func TestReferenceStringVariadicArrays(t *testing.T) {
	stringVariadicValues(t, []struct {
		call string
		want any
	}{
		{`concat('{1,NULL,2}'::integer[])`, `{1,NULL,2}`}, {`concat(VARIADIC '{1,NULL,2}'::integer[])`, "12"},
		{`concat_ws('|',VARIADIC '{1,NULL,2}'::integer[])`, "1|2"},
		{`concat_ws('|',VARIADIC '{{1,2},{3,4}}'::integer[])`, "1|2|3|4"},
		{`concat_ws('|',VARIADIC '[0:2]={1,NULL,2}'::integer[])`, "1|2"},
		{`concat(VARIADIC '{}'::text[])`, ""}, {`concat_ws('|',VARIADIC '{}'::text[])`, ""},
		{`concat(VARIADIC '{NULL,NULL}'::text[])`, ""}, {`concat(VARIADIC NULL::text[])`, nil},
		{`concat_ws('|',VARIADIC NULL::text[])`, nil}, {`concat_ws('|',VARIADIC '{"NULL",NULL,x}'::text[])`, "NULL|x"},
	})
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN sv_array_domain AS integer[]`, 0))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT concat_ws('|',VARIADIC '{1,NULL,2}'::sv_array_domain) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"1|2"}}))
	})
}

func TestReferenceStringVariadicOutput(t *testing.T) {
	stringVariadicValues(t, []struct {
		call string
		want any
	}{
		{`concat(true,false)`, "tf"}, {`concat(42::int4,5::numeric(5,2),'x'::char(3))`, "425.00x  "},
		{`quote_literal(true)`, `'true'`}, {`quote_literal(false)`, `'false'`},
		{`quote_literal(5::numeric(5,2))`, `'5.00'`}, {`quote_literal('x'::char(3))`, `'x'`},
		{`quote_nullable('x'::char(3))`, `'x'`}, {`quote_literal('x  '::varchar)`, `'x  '`},
		{`quote_literal('{1,NULL,2}'::integer[])`, `'{1,NULL,2}'`},
		{`concat('10.0.0.0/8'::cidr)`, "10.0.0.0/8"},
		{`quote_literal('10.0.0.0/8'::cidr)`, `'10.0.0.0/8'`},
		{`quote_nullable('10.0.0.0/8'::cidr)`, `'10.0.0.0/8'`},
		{`concat('x'::"char")`, "x"}, {`concat(E'\\200'::"char")`, `\200`},
	})
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// PostgreSQL's explicit CHAR-to-TEXT casts succeed; nested general CHAR
		// casts remain outside native admission. Assert that boundary, not a skip.
		for _, tc := range []struct {
			call     string
			position string
		}{
			{`quote_literal('x'::"char"::text)`, "27"},
			{`quote_nullable('x'::"char"::text)`, "28"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "SELECT " + tc.call + " AS value"
				if b.PG {
					formatReferenceBoth(t, p, b, c, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{"'x'"}}))
				} else {
					formatReferenceFailure(t, p, b, c, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported column type", Position: tc.position}, true)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN sv_output_char AS char(3)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN sv_output_element AS integer`, 0))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT concat('x'::sv_output_char),quote_literal('x'::sv_output_char),concat_ws('|',VARIADIC '{1,NULL,2}'::sv_output_element[])`, []string{"concat", "quote_literal", "concat_ws"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"x  ", "'x'", "1|2"}}))
		stringVariadicContract(t, p, b, c, `SELECT concat('Infinity'::float8,'NaN'::float8),quote_literal('Infinity'::float8),concat('{"x  "}'::char(3)[]),quote_literal('{"x  "}'::char(3)[])`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET bytea_output=hex`, 0))
		q := `SELECT concat('\x00015c'::bytea) AS raw,quote_literal('\x00015c'::bytea) AS quoted`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, setting := range []string{"hex", "escape", "hex"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET bytea_output='"+setting+"'", 0))
			raw, quoted := `\x00015c`, `E'\\x00015c'`
			if setting == "escape" {
				raw = `\000\001\\`
				quoted = `E'\\000\\001\\\\'`
			}
			formatReferenceBoth(t, p, b, c, fixture.Select(q, []string{"raw", "quoted"}, []string{"TEXT", "TEXT"}, [][]any{{raw, quoted}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"raw", "quoted"}, []string{"TEXT", "TEXT"}, [][]any{{raw, `E'\\x00015c'`}}))
		}
	})
}

func TestReferenceStringVariadicQuoting(t *testing.T) {
	stringVariadicValues(t, []struct {
		call string
		want any
	}{
		{`quote_ident('foo')`, "foo"}, {`quote_ident('_foo')`, "_foo"}, {`quote_ident('abort')`, "abort"},
		{`quote_ident('between')`, `"between"`}, {`quote_ident('authorization')`, `"authorization"`}, {`quote_ident('select')`, `"select"`},
		{`quote_ident('Foo bar')`, `"Foo bar"`}, {`quote_ident('')`, `""`}, {`quote_ident('a"b')`, `"a""b"`},
		{`quote_ident('é')`, `"é"`}, {`quote_ident('a$b')`, `"a$b"`}, {`quote_ident('a.b')`, `"a.b"`},
		{`quote_ident('` + strings.Repeat("a", 80) + `')`, strings.Repeat("a", 80)},
		{`quote_ident(NULL::text)`, nil}, {`quote_literal('')`, `''`}, {`quote_literal('O''Reilly')`, `'O''Reilly'`},
		{`quote_literal(E'a\\b')`, `E'a\\b'`}, {"quote_literal('é\nline')", "'é\nline'"},
		{`quote_literal(NULL::text)`, nil}, {`quote_nullable(NULL::text)`, "NULL"}, {`quote_nullable('NULL')`, `'NULL'`},
	})
}

func TestReferenceStringVariadicEncoding(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT pg_client_encoding() AS encoding`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		want := fixture.Select(q, []string{"encoding"}, []string{"NAME"}, [][]any{{"UTF8"}})
		for _, s := range []string{`SET client_encoding='UNICODE'`, `BEGIN`, `SAVEPOINT sv_encoding`, `SET LOCAL client_encoding='UTF8'`, `ROLLBACK TO sv_encoding`, `COMMIT`, `RESET client_encoding`} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(s, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, want)
		}
	})
}

func TestReferenceStringVariadicFeatureSmoke(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT concat(true,false,NULL) AS joined,concat_ws('|',VARIADIC '{1,NULL,2}'::integer[]) AS separated,quote_ident('a"b') AS identifier,quote_literal($1::text) AS literal,quote_nullable(NULL::text) AS nullable,pg_client_encoding() AS encoding`
		formatReferenceBoth(t, p, b, c, fixture.Select(q, []string{"joined", "separated", "identifier", "literal", "nullable", "encoding"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "NAME"}, [][]any{{"tf", "1|2", `"a""b"`, `E'O''Reilly\\path'`, "NULL", "UTF8"}}, `O'Reilly\path`))
		stringVariadicFirstExecution(t, p, b, c)
	})
}
