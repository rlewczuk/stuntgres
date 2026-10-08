//go:build reference

package reference_test

import "testing"

// Only inherited exclusions made obsolete by this string-family cutover are
// migrated here. Existing binary positive and unrelated exclusion suites remain.
func stringCoreReviewBinaryConsumptionReference(t *testing.T, p *referencePair, oracle bool) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(t, referenceExec("CREATE TABLE t(v BYTEA,s TEXT)", 0))
	run(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
	run(t, referenceExec(`INSERT INTO t VALUES('\x41',' x ')`, 1))
	for _, tc := range []struct {
		q    string
		want any
	}{
		{"SELECT btrim(NULL,NULL) AS v", nil},
		{"SELECT ltrim(' x ') AS v", "x "},
		{"SELECT rtrim(s) AS v FROM t", " x"},
		{"SELECT v||s AS v FROM t", `\x41 x `},
		{"SELECT s||v AS v FROM t", ` x \x41`},
		{"SELECT 'a'||'b' AS v FROM t", "ab"},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
			if prepared {
				s = referencePrepared(s)
			}
			run(t, s)
		}
	}
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT TRIM(v) FROM t", referenceDiagnostic{Code: "42883", Message: "function pg_catalog.btrim(bytea) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"INSERT INTO dst SELECT 'a'||'b' FROM t", referenceDiagnostic{Code: "42804", Message: `column "v" is of type bytea but expression is of type text`, Position: "24", Hint: "You will need to rewrite or cast the expression."}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(tc.q, tc.want)
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				run(t, s)
			}
			run(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, nil))
		})
	}
}
