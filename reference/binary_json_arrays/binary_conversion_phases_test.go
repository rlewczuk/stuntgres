package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryConversionPhases(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, code, message string
			pos              int
		}{
			{`SELECT encode(NULL,'\x0'::bytea)`, "22023", "invalid hexadecimal data: odd number of digits", 20},
			{`SELECT convert(NULL,missing,missing2)`, "42703", `column "missing" does not exist`, 21},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fmt.Sprint(tc.pos)
				fixture.StringEncodingFailure(t, p, b, c, tc.q, d, true)
			})
		}
		t.Run("missing_direct_parameter", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
			d.Position = "16"
			// PostgreSQL's no-bind simple-query route rejects the first parameter.
			// Native's inherited direct admission analyzes before supplied-count
			// validation; it therefore exposes the independently specified repeated
			// formal-role conflict. This is a transport boundary, not codec behavior.
			if !b.PG {
				d = fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
				d.Detail = "bytea versus name"
				d.Position = "19"
			}
			s := fixture.ErrorDiagnostic("SELECT convert($1,$1,'UTF8')", d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
		t.Run("repeated_parameter_prepare", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
			d.Detail = "bytea versus name"
			d.Position = "19"
			s := fixture.Prepared(fixture.ErrorDiagnostic("SELECT convert($1,$1,'UTF8')", d))
			s.SelectRows = true
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
		q := "SELECT encode(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + ")"
		d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
		d.Position = "8"
		fixture.StringEncodingFailure(t, p, b, c, q, d, true)
		for _, tc := range []struct {
			q, typ string
			want   any
		}{
			{`SELECT encode(decode($1,$1),'hex') AS v`, "TEXT", ""},
			{`SELECT convert_to(NULL::text,'bad') AS v`, "BYTEA", nil},
			{`SELECT convert_from(NULL::bytea,'bad') AS v`, "TEXT", nil},
			{`SELECT decode(NULL::text,'bad') AS v`, "BYTEA", nil},
			{`SELECT convert(NULL::bytea,'bad','worse') AS v`, "BYTEA", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				if strings.Contains(tc.q, "$1") {
					binaryConversionBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{nil}}, nil))
					return
				}
				binaryConversionBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE bc_phase(b bytea,s text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO bc_phase VALUES('\x00','0q')`, 1))
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT decode(s,'hex') FROM bc_phase", "22023", `invalid hexadecimal digit: "q"`},
			{"SELECT convert_from(b,'UTF8') FROM bc_phase", "22021", `invalid byte sequence for encoding "UTF8": 0x00`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				fixture.StringEncodingFailure(t, p, b, c, tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
		// Explicit conflict diagnostics and analysis phase match PostgreSQL18.0.
		q = `SELECT decode('61' COLLATE "C",'hex' COLLATE "POSIX")`
		d = fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
		d.Position = "38"
		fixture.StringEncodingFailure(t, p, b, c, q, d, true)
	})
}
