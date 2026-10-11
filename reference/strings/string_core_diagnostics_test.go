package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

type stringCoreDiagnosticCase struct {
	q       string
	want    fixture.Diagnostic
	prepare bool
}

func stringCoreDiagnosticCases() []stringCoreDiagnosticCase {
	return []stringCoreDiagnosticCase{
		{"SELECT btrim(1)", fixture.Diagnostic{Code: "42883", Message: "function btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim(1)", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize(1)", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.normalize(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 IS NORMALIZED", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.is_normalized(integer) does not exist", Position: "10", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 || 2", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer || integer", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize('x','NFC')", fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"'NFC'\"", Position: "22", Hint: ""}, true},
		{"SELECT normalize('x', BAD)", fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "23", Hint: ""}, true},
		{"SELECT 'x' IS BAD NORMALIZED", fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "15", Hint: ""}, true},
		{"SELECT \"normalize\"('x','bad')", fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','bad')", fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x',' NFC')", fixture.Diagnostic{Code: "22023", Message: "invalid normalization form:  NFC", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','')", fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: ", Position: "", Hint: ""}, false},
		{"SELECT btrim(NULL,1)", fixture.Diagnostic{Code: "42883", Message: "function btrim(unknown, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim()", fixture.Diagnostic{Code: "42883", Message: "function btrim() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim('x','y','z')", fixture.Diagnostic{Code: "42883", Message: "function btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim('x','y','z')", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT pg_catalog.trim('x')", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT \"trim\"('x')", fixture.Diagnostic{Code: "42883", Message: "function trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
	}
}
func stringCoreDiagnosticStep(tc stringCoreDiagnosticCase, prepared bool) fixture.Step {
	s := fixture.ImplicitError(tc.q, tc.want)
	if prepared {
		s = fixture.Prepared(s)
		s.ErrorStage = "execute"
		if tc.prepare {
			s.ErrorStage = "prepare"
		}
	}
	return s
}
func TestReferenceStringCoreDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range stringCoreDiagnosticCases() {
		t.Run(tc.q, func(t *testing.T) {
			p.Step(t, stringCoreDiagnosticStep(tc, false))
			p.Step(t, stringCoreDiagnosticStep(tc, true))
		})
	}
	stringCoreReferenceDiagnosticParameters(t, p)
}
