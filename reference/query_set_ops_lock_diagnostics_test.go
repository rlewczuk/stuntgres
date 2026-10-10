package reference_test

import "testing"

func TestReferenceQuerySetOpsLockDiagnostics(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsSeed(t, p, b)
		check := func(q, mode string) {
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "FOR " + mode + " is not allowed with UNION/INTERSECT/EXCEPT"}
			s := referenceErrorDiagnostic(q, want)
			s.selectRows = true
			querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
		}
		for _, op := range []string{"UNION", "UNION DISTINCT", "UNION ALL", "INTERSECT", "INTERSECT DISTINCT", "INTERSECT ALL", "EXCEPT", "EXCEPT DISTINCT", "EXCEPT ALL"} {
			for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
				for _, q := range []string{
					"SELECT 1 " + op + " SELECT 2,3 FOR " + mode,
					"(SELECT 1 FOR " + mode + ") " + op + " SELECT 2,3",
					"SELECT * FROM (SELECT v FROM sl " + op + " SELECT v FROM sr) d FOR " + mode,
				} {
					check(q, mode)
				}
			}
		}
		check("SELECT TRUE UNION SELECT 1 FOR SHARE", "SHARE")
		check("SELECT 1 UNION (SELECT 2,3 FOR SHARE)", "SHARE")
	})
}
