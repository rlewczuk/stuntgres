package reference_test

import "testing"

type stringCoreDiagnosticCase struct {
	q       string
	want    referenceDiagnostic
	prepare bool
}

func stringCoreDiagnosticCases() []stringCoreDiagnosticCase {
	return []stringCoreDiagnosticCase{
		{"SELECT btrim(1)", referenceDiagnostic{Code: "42883", Message: "function btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim(1)", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.btrim(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize(1)", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.normalize(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 IS NORMALIZED", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.is_normalized(integer) does not exist", Position: "10", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT 1 || 2", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer || integer", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT normalize('x','NFC')", referenceDiagnostic{Code: "42601", Message: "syntax error at or near \"'NFC'\"", Position: "22", Hint: ""}, true},
		{"SELECT normalize('x', BAD)", referenceDiagnostic{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "23", Hint: ""}, true},
		{"SELECT 'x' IS BAD NORMALIZED", referenceDiagnostic{Code: "42601", Message: "syntax error at or near \"BAD\"", Position: "15", Hint: ""}, true},
		{"SELECT \"normalize\"('x','bad')", referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','bad')", referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x',' NFC')", referenceDiagnostic{Code: "22023", Message: "invalid normalization form:  NFC", Position: "", Hint: ""}, false},
		{"SELECT is_normalized('x','')", referenceDiagnostic{Code: "22023", Message: "invalid normalization form: ", Position: "", Hint: ""}, false},
		{"SELECT btrim(NULL,1)", referenceDiagnostic{Code: "42883", Message: "function btrim(unknown, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim()", referenceDiagnostic{Code: "42883", Message: "function btrim() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT btrim('x','y','z')", referenceDiagnostic{Code: "42883", Message: "function btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT trim('x','y','z')", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.btrim(unknown, unknown, unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT pg_catalog.trim('x')", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT \"trim\"('x')", referenceDiagnostic{Code: "42883", Message: "function trim(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
	}
}
func stringCoreDiagnosticStep(tc stringCoreDiagnosticCase, prepared bool) referenceStep {
	s := referenceImplicitError(tc.q, tc.want)
	if prepared {
		s = referencePrepared(s)
		s.errorStage = "execute"
		if tc.prepare {
			s.errorStage = "prepare"
		}
	}
	return s
}
func TestReferenceStringCoreDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range stringCoreDiagnosticCases() {
		t.Run(tc.q, func(t *testing.T) {
			p.step(t, stringCoreDiagnosticStep(tc, false))
			p.step(t, stringCoreDiagnosticStep(tc, true))
		})
	}
	stringCoreReferenceDiagnosticParameters(t, p)
}
