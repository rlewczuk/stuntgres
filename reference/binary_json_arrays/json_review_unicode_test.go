package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 src/common/jsonapi.c distinguishes physical EOF (JSON_INVALID_TOKEN)
// from a present nonhex digit (JSON_UNICODE_ESCAPE_FORMAT). These inputs have no
// closing JSON quote. Expectations are validated against pinned PostgreSQL 18.0.
func TestReferenceJSONReviewUnicodePhysicalEOF(t *testing.T) {
	cases := []jsonReferenceInvalid{
		{"zero_digits", `"\u`, `Token ""\u" is invalid.`, `JSON data, line 1: "\u`},
		{"one_digit", `"\u0`, `Token ""\u0" is invalid.`, `JSON data, line 1: "\u0`},
		{"two_digits", `"\u00`, `Token ""\u00" is invalid.`, `JSON data, line 1: "\u00`},
		{"three_digits", `"\u000`, `Token ""\u000" is invalid.`, `JSON data, line 1: "\u000`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
						want := jsonReferenceDiagnostic(tc)
						want.Position = "8"
						if prefix != "SELECT " {
							want.Position = "14"
						}
						s := fixture.ErrorDiagnostic(prefix+jsonReferenceQuote(tc.input)+"::json", want)
						s.SelectRows, s.ErrorStage = true, "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					for _, input := range []any{tc.input, []byte(tc.input)} {
						want := jsonReferenceDiagnostic(tc)
						want.Context += "\nunnamed portal parameter $1 = '...'"
						s := fixture.ErrorDiagnostic("SELECT $1::json", want, input)
						s.SelectRows, s.ErrorStage = true, "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
				}
			}
		})
	}
}
