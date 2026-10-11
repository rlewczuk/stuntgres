package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONContextBoundaries(t *testing.T) {
	cases := []jsonReferenceInvalid{
		{"bytes49", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", `Token "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" is invalid.`, "JSON data, line 1: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"bytes50", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", `Token "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" is invalid.`, "JSON data, line 1: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"bytes53", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", `Token "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" is invalid.`, "JSON data, line 1: ...xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"multibyte", "ééééééééééééééééééééééééééé", `Token "ééééééééééééééééééééééééééé" is invalid.`, "JSON data, line 1: ...éééééééééééééééééééééééé"},
		{"cr_not_lf", "[\r1,]", `Expected JSON value, but found "]".`, "JSON data, line 1: [\r1,]"},
		{"eof_after_lf", "[1,\n  ", "The input string ended unexpectedly.", "JSON data, line 2:   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					want := jsonReferenceDiagnostic(tc)
					want.Position = "8"
					s := fixture.ErrorDiagnostic("SELECT "+jsonReferenceQuote(tc.input)+"::json", want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}

func TestReferenceJSONBoundEncoding(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  any
		want fixture.Diagnostic
	}{
		{"invalid_utf8_string", string([]byte{0xff}), fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`, Context: "unnamed portal parameter $1"}},
		{"invalid_utf8_bytes", []byte{0xff}, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`, Context: "unnamed portal parameter $1"}},
		{"nul_string", "\x00", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`, Context: "unnamed portal parameter $1"}},
		{"nul_bytes", []byte{0}, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`, Context: "unnamed portal parameter $1"}},
		{"empty_bytes", []byte{}, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: "The input string ended unexpectedly.", Context: "JSON data, line 1: \nunnamed portal parameter $1 = ''"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						s := fixture.ErrorDiagnostic("SELECT $1::json", tc.want, tc.arg)
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					})
				}
			}
		})
	}
}
