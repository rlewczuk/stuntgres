package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func macaddrDiagnostic(input, code string) fixture.Diagnostic {
	message := `invalid input syntax for type macaddr: "` + input + `"`
	if code == "22003" {
		message = `invalid octet value in "macaddr" value: "` + input + `"`
	}
	return fixture.ControlsReferenceDiagnostic(code, message)
}

var macaddrInvalid = []struct{ input, code string }{
	{"", "22P02"}, {"0800:2b01:0203", "22P02"}, {"not even close", "22P02"},
	{"08:00:2b:01:02:ZZ", "22P02"}, {"08:00:2b:01:02", "22P02"},
	{"08:00-2b:01:02:03", "22P02"}, {"08:00:2b:01:02:03:04", "22P02"},
	{"08:00:2b:01:02:03junk", "22P02"}, {"08:00:2b:01:02:03:04:05", "22P02"},
	{"100:00:2b:01:02:03", "22003"}, {"-1:00:2b:01:02:03", "22003"},
	{"08:00:2b:01:02:100", "22003"}, {"100:00:2b:01:02:03junk", "22P02"},
	{"08 :00:2b:01:02:03", "22P02"}, {"\u00a008:00:2b:01:02:03", "22P02"},
	{"08:00:2b:01:02:03\u00a0", "22P02"}, {"08:00:2b:01:02:03 \tjunk", "22P02"},
	{"bad\"\\\n'", "22P02"},
	{"08002b0102", "22P02"},
	{"ffffffff:00:00:00:00:00", "22003"},
	{"-100000001:00:00:00:00:00", "22003"},
	{"ffffffffffffffff:00:00:00:00:00", "22003"},
	{"10000000000000000:00:00:00:00:00", "22003"},
	{"-10000000000000000:00:00:00:00:00", "22003"},
	{strings.Repeat("f", 100) + ":00:00:00:00:00", "22003"},
	{"-" + strings.Repeat("f", 100) + ":00:00:00:00:00", "22003"},
}

func TestReferenceMACADDRScanner(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		valid := [][2]string{
			{"8:0:2B:1:2:3", macaddrSample}, {"+08:+00:+2b:+01:+02:+03", macaddrSample},
			{"0x08:0x00:0x2b:0x01:0x02:0x03", macaddrSample},
			{"0008:0000:002b:0001:0002:0003", macaddrSample},
			{"-0:0:0:0:0:0", "00:00:00:00:00:00"}, {"08002b01023", macaddrSample},
			// Pinned PostgreSQL 18.6 observations: scanf width includes sign
			// and prefix characters; an incomplete 0x still converts zero.
			{"8002b010203", "80:02:b0:10:20:03"},
			{"08002b0102+3", macaddrSample},
			{"08002b01020x", "08:00:2b:01:02:00"},
			{"0x002b010203", "00:00:2b:01:02:03"},
			{"0x:00:00:00:00:00", "00:00:00:00:00:00"},
			{"0x0x0x0x0x0x", "00:00:00:00:00:00"},
			{"+8002b010203", macaddrSample},
			{"08002b:01023", macaddrSample},
			{"0800-2b01-0203", macaddrSample},
			{"-ffffffff:00:00:00:00:00", "01:00:00:00:00:00"},
			{"100000000:00:00:00:00:00", "00:00:00:00:00:00"},
			{"-100000000:00:00:00:00:00", "00:00:00:00:00:00"},
			{"100000001:00:00:00:00:00", "01:00:00:00:00:00"},
			{"-ffffffffffffffff:00:00:00:00:00", "01:00:00:00:00:00"},
		}
		for _, ws := range []string{" ", "\t", "\n", "\r", "\v", "\f"} {
			valid = append(valid, [2]string{ws + macaddrSample + ws, macaddrSample}, [2]string{"08:" + ws + "00:" + ws + "2b:" + ws + "01:" + ws + "02:" + ws + "03", macaddrSample})
		}
		for _, v := range valid {
			for _, prepared := range []bool{false, true} {
				s := macaddrValue("SELECT $1::macaddr AS v", v[1], v[0])
				s.Prepared = prepared
				run(s)
			}
		}
		for _, v := range macaddrInvalid {
			d := macaddrDiagnostic(v.input, v.code)
			d.Context = "unnamed portal parameter $1 = '...'"
			if v.input == "" {
				d.Context = "unnamed portal parameter $1 = ''"
			}
			s := fixture.ErrorDiagnostic("SELECT $1::macaddr", d, v.input)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
	})
}

func TestReferenceMACADDRDiagnostics(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_errors(id integer,v macaddr)", 0))
		run(fixture.Exec("INSERT INTO mac_errors VALUES(1,'08002b010203')", 1))
		run(fixture.Exec("CREATE TABLE mac_empty(v macaddr)", 0))
		for _, v := range macaddrInvalid {
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, typed := range []bool{false, true} {
					q := prefix + "SELECT " + macaddrQuote(v.input) + "::macaddr"
					pos := 8
					if typed {
						q = prefix + "SELECT MACADDR " + macaddrQuote(v.input)
						pos = 16
					}
					d := macaddrDiagnostic(v.input, v.code)
					d.Position = fixture.Position(pos + utf8.RuneCountInString(prefix))
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = query
						s.ErrorStage = "execute"
						run(s)
						s.Prepared = true
						s.ErrorStage = "prepare"
						run(s)
					}
				}
			}
			for _, arg := range []any{v.input, []byte(v.input)} {
				d := macaddrDiagnostic(v.input, v.code)
				d.Context = "unnamed portal parameter $1 = '...'"
				if v.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT $1::macaddr", d, arg)
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				}
			}
		}
		for _, q := range []string{"SELECT 'bad'::macaddr FROM mac_errors WHERE FALSE", "SELECT 'bad'::macaddr FROM mac_empty"} {
			d := macaddrDiagnostic("bad", "22P02")
			d.Position = "8"
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 'bad'::text::macaddr", macaddrDiagnostic("bad", "22P02"))
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		unchanged := fixture.Select("SELECT id,v FROM mac_errors ORDER BY id", []string{"id", "v"}, []string{"INT4", "MACADDR"}, [][]any{{int64(1), macaddrSample}})
		q := "INSERT INTO mac_errors VALUES(2,'ffffffffffff'),(3,'bad')"
		d := macaddrDiagnostic("bad", "22P02")
		d.Position = fixture.Position(strings.Index(q, "'bad'") + 1)
		run(fixture.ErrorDiagnostic(q, d))
		run(unchanged)
		for _, prepared := range []bool{false, true} {
			d = macaddrDiagnostic("bad", "22P02")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO mac_errors VALUES(2,$1),(3,$2)", d, "ffffffffffff", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(unchanged)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := b.DB.ExecContext(ctx, "INSERT INTO mac_errors VALUES(4,$1)", macaddrSample)
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(unchanged)
		}
	})
}
