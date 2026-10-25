package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func macaddr8Diagnostic(input string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type macaddr8: "`+input+`"`)
}

func macaddr8ParameterDiagnostic(input string, slot string) fixture.Diagnostic {
	d := macaddr8Diagnostic(input)
	d.Context = "unnamed portal parameter $" + slot + " = '...'"
	if input == "" {
		d.Context = "unnamed portal parameter $" + slot + " = ''"
	}
	return d
}

var macaddr8Invalid = []string{
	"", " \t\r\n\v\f", "08002b0102", "08002b01020304", "08002b010203040506",
	"08:00-2b:01:02:03", "08::00:2b:01:02:03", "08:00:2g:01:02:03",
	"0:800:2b:01:02:03", "8:0:2b:1:2:3", "+08:00:2b:01:02:03",
	"-1:00:2b:01:02:03", "0x08:00:2b:01:02:03", "100:00:2b:01:02:03",
	"08 :00:2b:01:02:03", "08:\t00:2b:01:02:03", "\u00a008:00:2b:01:02:03",
	"08:00:2b:01:02:03\u00a0", "08002b010203xx", "08002b010203 x",
	"08:00:2b:01:02:03-", "08002b0102030405xx", "bad\"\\\n'",
}

func TestReferenceMACADDR8Scanner(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Release-source predictions: the pinned oracle must settle these edges.
		valid := [][2]string{{"08002b010203x", macaddr8Six}, {"08002b010203f", macaddr8Six}, {"08002b0102030405x", macaddr8Eight}, {"08002b0102030405f", macaddr8Eight}, {"08002b010203:", macaddr8Six}, {"08:00:2b:01:02:03:", macaddr8Six}, {"08:00:2b:01:02:03::", macaddr8Six}, {"08002b0102030405:", macaddr8Eight}, {"08:00:2b:01:02:03:04:05::", macaddr8Eight}}
		for _, ws := range []string{" ", "\t", "\n", "\r", "\v", "\f"} {
			valid = append(valid, [2]string{ws + "08002b010203" + ws, macaddr8Six}, [2]string{ws + "08002b0102030405" + ws, macaddr8Eight})
		}
		for _, v := range valid {
			for _, prepared := range []bool{false, true} {
				s := macaddr8Value(b.PG, "SELECT $1::macaddr8 AS v", v[1], v[0])
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, input := range macaddr8Invalid {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT $1::macaddr8", macaddr8ParameterDiagnostic(input, "1"), input)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

func TestReferenceMACADDR8Diagnostics(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac8_errors(id integer,v macaddr8)", 0))
		run(fixture.Exec("INSERT INTO mac8_errors VALUES(1,'08002b010203')", 1))
		run(fixture.Exec("CREATE TABLE mac8_empty(v macaddr8)", 0))
		for _, input := range macaddr8Invalid {
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, tc := range []struct {
					q   string
					pos int
				}{{"SELECT " + macaddrQuote(input) + "::macaddr8", 8}, {"SELECT MACADDR8 " + macaddrQuote(input), 17}, {"SELECT pg_catalog.macaddr8 " + macaddrQuote(input), 28}} {
					d := macaddr8Diagnostic(input)
					d.Position = fixture.Position(tc.pos + utf8.RuneCountInString(prefix))
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(prefix+tc.q, d)
						s.SelectRows = query
						s.ErrorStage = "execute"
						run(s)
						s.Prepared = true
						s.ErrorStage = "prepare"
						run(s)
					}
				}
			}
			for _, arg := range []any{input, []byte(input)} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT $1::macaddr8", macaddr8ParameterDiagnostic(input, "1"), arg)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			}
		}
		for _, q := range []string{"SELECT 'bad'::macaddr8 FROM mac8_errors WHERE FALSE", "SELECT 'bad'::macaddr8 FROM mac8_empty"} {
			d := macaddr8Diagnostic("bad")
			d.Position = "8"
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 'bad'::text::macaddr8", macaddr8Diagnostic("bad"))
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		unchanged := macaddr8Select(b.PG, "SELECT id,v FROM mac8_errors ORDER BY id", []string{"id", "v"}, []string{"INT4", "MACADDR8"}, [][]any{{int64(1), macaddr8Six}})
		q := "INSERT INTO mac8_errors VALUES(2,'ffffffffffffffff'),(3,'bad')"
		d := macaddr8Diagnostic("bad")
		d.Position = fixture.Position(strings.Index(q, "'bad'") + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			run(s)
			run(unchanged)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO mac8_errors VALUES(2,$1),(3,$2)", macaddr8ParameterDiagnostic("bad", "2"), "ffffffffffffffff", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(unchanged)
		}
	})
}
