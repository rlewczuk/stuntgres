package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryConversionTyping(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{`convert_to('é '::char(3),'LATIN1')`, "BYTEA", "\xe9"}, {`convert_to('é '::varchar(3),'LATIN1')`, "BYTEA", "\xe9 "},
			{`convert_from('\xe9'::bytea,'LATIN1'::text)`, "TEXT", "é"}, {`decode('ff'::char(4),'hex')`, "BYTEA", "\xff"},
			{`pg_catalog.encode('a'::bytea,'hex')`, "TEXT", "61"}, {`"encode"('a'::bytea,'hex')`, "TEXT", "61"},
		} {
			t.Run(tc.expr, func(t *testing.T) { binaryConversionValue(t, p, b, c, tc.expr, tc.typ, tc.want) })
		}
		variadicDiagnostic := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ","`)
		variadicDiagnostic.Position = "28"
		fixture.StringEncodingFailure(t, p, b, c, `SELECT decode(VARIADIC 'ff','hex')`, variadicDiagnostic, true)
		for _, tc := range []struct{ call, signature string }{
			{"convert()", "convert()"}, {"convert('a','UTF8')", "convert(unknown, unknown)"}, {"convert_from(1,'UTF8')", "convert_from(integer, unknown)"}, {"convert_to(1,'UTF8')", "convert_to(integer, unknown)"},
			{"encode('a'::text,'hex')", "encode(text, unknown)"}, {"decode('a'::bytea,'hex')", "decode(bytea, unknown)"}, {`"ENCODE"('a','hex')`, "ENCODE(unknown, unknown)"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fmt.Sprint(8 + len([]rune(prefix)))
					fixture.StringEncodingFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, tc := range []struct {
			q, code, message string
			pos              int
		}{
			{`SELECT no_bc_schema.decode('aa','hex')`, "3F000", `schema "no_bc_schema" does not exist`, 8},
			{`SELECT encode(NULL,missing)`, "42703", `column "missing" does not exist`, 20},
			{`SELECT encode('\x0'::bytea,'hex')`, "22023", "invalid hexadecimal data: odd number of digits", 15},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fmt.Sprint(tc.pos)
				fixture.StringEncodingFailure(t, p, b, c, tc.q, d, true)
			})
		}
		name := strings.Repeat("é", 32)
		d := fixture.ControlsReferenceDiagnostic("22023", `invalid destination encoding name "`+strings.Repeat("é", 31)+`"`)
		fixture.StringEncodingFailure(t, p, b, c, "SELECT convert_to('a',$1)", d, false, name)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN bc_text AS text", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN bc_bytes AS bytea", 0))
		binaryConversionValue(t, p, b, c, `encode('a'::bc_bytes,'hex'::bc_text)`, "TEXT", "61")
		for _, q := range []string{`SELECT convert_to($1,'LATIN1') AS v`, `SELECT decode($1,'hex') AS v`} {
			for _, raw := range []string{"A\x00", "A\xff"} {
				bytes := "0x00"
				if raw[1] == 0xff {
					bytes = "0xff"
				}
				d := fixture.ControlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+bytes)
				d.Context = "unnamed portal parameter $1"
				fixture.StringEncodingFailure(t, p, b, c, q, d, false, raw)
			}
		}
	})
}
func TestReferenceBinaryConversionHeld(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ        string
			args          []any
			want          any
			badArgs       []any
			code, message string
		}{
			{"SELECT convert($1,$2,$3) AS v", "BYTEA", []any{[]byte{0xe9}, "LATIN1", "UTF8"}, "é", []any{[]byte{0xff}, "UTF8", "LATIN1"}, "22021", `invalid byte sequence for encoding "UTF8": 0xff`},
			{"SELECT convert_from($1,$2) AS v", "TEXT", []any{[]byte{0xe9}, "LATIN1"}, "é", []any{[]byte{0}, "LATIN1"}, "22021", `invalid byte sequence for encoding "LATIN1": 0x00`},
			{"SELECT convert_to($1,$2) AS v", "BYTEA", []any{"é", "LATIN1"}, "\xe9", []any{"€", "LATIN1"}, "22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`},
			{"SELECT encode($1,$2) AS v", "TEXT", []any{[]byte{0xff}, "hex"}, "ff", []any{[]byte{0xff}, "nope"}, "22023", `unrecognized encoding: "nope"`},
			{"SELECT decode($1,$2) AS v", "BYTEA", []any{"ff", "hex"}, "\xff", []any{"0q", "hex"}, "22023", `invalid hexadecimal digit: "q"`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				valid := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
				var frames []fixture.Step
				frames = append(frames, valid)
				for i := range tc.args {
					a := append([]any(nil), tc.args...)
					a[i] = nil
					frames = append(frames, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{nil}}, a...))
				}
				bad := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), tc.badArgs...)
				bad.SelectRows = true
				bad.ErrorStage = "execute"
				frames = append(frames, bad, valid)
				binaryConversionHeld(t, p, b, c, tc.q, frames)
			})
		}
	})
}
