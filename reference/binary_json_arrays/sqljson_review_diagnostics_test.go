package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONClauseBoundaries(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prefix := range []string{"SELECT ", "/*é😀*/ SELECT "} {
			for _, tc := range []struct{ expr, token string }{
				{`JSON_OBJECT(ARRAY['a','1'] RETURNING text)`, `RETURNING`},
				{`JSON_OBJECT(ARRAY['a','1','a','2'] WITH UNIQUE KEYS)`, `WITH`},
				{`JSON_OBJECT(ARRAY['a','1'] FORMAT JSON)`, `FORMAT`},
				{`JSON_OBJECT(ARRAY['a','1'] ABSENT ON NULL)`, `ABSENT`},
				{`JSON_OBJECT(ARRAY['a'], ARRAY['1'] RETURNING text)`, `RETURNING`},
				{`JSON_OBJECT(ARRAY['a','1'] NULL ON NULL)`, `NULL`},
				{`JSON_OBJECT(ARRAY['a','1'] WITHOUT UNIQUE KEYS)`, `WITHOUT`},
				{`JSON_OBJECT(NULL ON NULL)`, `ON`},
				{`JSON_OBJECT(ABSENT ON NULL)`, `ON`},
				{`JSON_OBJECT(WITH UNIQUE KEYS)`, `WITH`},
				{`JSON_OBJECT(WITH UNIQUE)`, `WITH`},
				{`JSON_OBJECT(WITHOUT UNIQUE KEYS)`, `UNIQUE`},
				{`JSON_OBJECT(WITHOUT UNIQUE)`, `UNIQUE`},
				{`JSON_OBJECT(ABSENT ON NULL RETURNING text)`, `ON`},
				{`JSON_OBJECT(WITH UNIQUE KEYS RETURNING text)`, `WITH`},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
					offset := strings.Index(tc.expr, tc.token)
					if tc.token == "ON" {
						offset = strings.Index(tc.expr, " ON ") + 1
					}
					d.Position = fixture.Position(len([]rune(prefix)) + len([]rune(tc.expr[:offset])) + 1)
					fixture.SQLJSONDiagnostic(t, p, b, c, prefix+tc.expr+";", d, true)
				})
			}
		}
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_OBJECT(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`JSON_OBJECT(ARRAY['a'], ARRAY['1'])`, "JSON", `{"a" : "1"}`},
			{`pg_catalog.json_object(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`"json_object"(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`JSON_OBJECT()`, "JSON", `{}`},
			{`JSON_OBJECT(RETURNING json)`, "JSON", `{}`},
			{`JSON_OBJECT(RETURNING jsonb)`, "JSONB", `{}`},
			{`JSON_OBJECT(RETURNING text)`, "TEXT", `{}`},
			{`JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `{}`},
			{`JSON_OBJECT('a':1 ABSENT ON NULL WITH UNIQUE KEYS RETURNING text)`, "TEXT", `{"a" : 1}`},
			{`JSON_OBJECT('a' VALUE 1 NULL ON NULL WITHOUT UNIQUE KEYS RETURNING text)`, "TEXT", `{"a" : 1}`},
		})
	})
}

func TestReferenceSQLJSONOutputEncoding(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			`CREATE DOMAIN sqj_encoding_text AS text`,
			`CREATE DOMAIN sqj_encoding_bytea AS bytea`,
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, prefix := range []string{"SELECT ", "/*é😀*/ SELECT "} {
			for _, route := range []struct{ name, head string }{
				{"argument", `JSON_ARRAY(1 RETURNING `},
				{"query", `JSON_ARRAY(SELECT 1 RETURNING `},
				{"serialize", `JSON_SERIALIZE('{}' RETURNING `},
				{"object", `JSON_OBJECT('a':1 RETURNING `},
				{"empty object", `JSON_OBJECT(RETURNING `},
			} {
				for _, typ := range []string{"text", "varchar(32)", "json", "jsonb", "sqj_encoding_text", "sqj_encoding_bytea"} {
					for _, encoding := range []string{"UTF8", "UTF16", "UTF32"} {
						expr := route.head + typ + " FORMAT JSON ENCODING " + encoding + ")"
						t.Run(prefix+route.name+"/"+typ+"/"+encoding, func(t *testing.T) {
							// Explicit encoding is rejected before unsupported-encoding
							// and SERIALIZE return-category checks. Domains retain their
							// declared identity, even when their base type is BYTEA.
							d := fixture.ControlsReferenceDiagnostic("0A000", "cannot set JSON encoding for non-bytea output types")
							d.Position = fixture.Position(len([]rune(prefix)) + len([]rune(expr[:strings.Index(expr, "FORMAT")])) + 1)
							fixture.SQLJSONDiagnostic(t, p, b, c, prefix+expr+";", d, true)
						})
					}
				}
				for _, encoding := range []string{"UTF16", "UTF32"} {
					expr := route.head + "bytea FORMAT JSON ENCODING " + encoding + ")"
					t.Run(prefix+route.name+"/bytea/"+encoding, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("0A000", "unsupported JSON encoding")
						d.Hint = "Only UTF8 JSON encoding is supported."
						d.Position = fixture.Position(len([]rune(prefix)) + len([]rune(expr[:strings.Index(expr, "FORMAT")])) + 1)
						fixture.SQLJSONDiagnostic(t, p, b, c, prefix+expr+";", d, true)
					})
				}
			}
		}
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY(1 RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `[1]`},
			{`JSON_ARRAY(SELECT 1 RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `[1]`},
			{`JSON_ARRAY(SELECT 1 WHERE false RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", nil},
			{`JSON_ARRAY(SELECT NULL::int RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `[]`},
			{`JSON_SERIALIZE('{}' RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `{}`},
			{`JSON_OBJECT('a':1 RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `{"a" : 1}`},
			{`JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8)`, "BYTEA", `{}`},
			{`JSON_ARRAY(1 RETURNING text FORMAT JSON)`, "TEXT", `[1]`},
			{`JSON_ARRAY(SELECT 1 RETURNING text FORMAT JSON)`, "TEXT", `[1]`},
			{`JSON_SERIALIZE('{}' RETURNING text FORMAT JSON)`, "TEXT", `{}`},
			{`JSON_OBJECT('a':1 RETURNING text FORMAT JSON)`, "TEXT", `{"a" : 1}`},
		})
	})
}
