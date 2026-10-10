package reference_test

import "testing"

// Complete logical diagnostics from pinned18.0 (root artifact8).
// Unspecified fields are asserted empty by referenceAssertDiagnostic.
func TestReferenceQuerySetOpsEqualityDiagnostics(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsSeed(t, p, b)
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sj(v JSON)", 0),
			referenceExec("CREATE TABLE sca(v TEXT COLLATE \"C\")", 0),
			referenceExec("CREATE TABLE scb(v TEXT COLLATE \"POSIX\")", 0),
			referenceExec("CREATE TABLE sm(a NUMERIC(6,2),b NUMERIC(7,3),c VARCHAR(5),d VARCHAR(7),n INT NOT NULL)", 0),
			referenceExec("CREATE DOMAIN set_json_domain AS JSON", 0),
			referenceExec("CREATE TYPE set_enum AS ENUM('a','b')", 0),
			referenceExec("CREATE TYPE set_enum_other AS ENUM('a','b')", 0),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		for _, tc := range []struct {
			q             string
			want          referenceDiagnostic
			preparedStage string
		}{
			{"SELECT NULL::JSON UNION SELECT NULL::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj UNION SELECT v FROM sj", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain UNION SELECT '{}'::set_json_domain", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] UNION SELECT '{\"{}\"}'::JSON[]", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca UNION SELECT v FROM scb", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "32", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON UNION DISTINCT SELECT '{}'::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON UNION DISTINCT SELECT NULL::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj UNION DISTINCT SELECT v FROM sj", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain UNION DISTINCT SELECT '{}'::set_json_domain", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] UNION DISTINCT SELECT '{\"{}\"}'::JSON[]", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca UNION DISTINCT SELECT v FROM scb", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "41", Constraint: ""}, "prepare"},
			{"SELECT TRUE UNION DISTINCT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types boolean and integer cannot be matched", Position: "35", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT UNION DISTINCT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "40", Constraint: ""}, "prepare"},
			{"SELECT TRUE UNION ALL SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types boolean and integer cannot be matched", Position: "30", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT UNION ALL SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "35", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON INTERSECT SELECT '{}'::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON INTERSECT SELECT NULL::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj INTERSECT SELECT v FROM sj", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain INTERSECT SELECT '{}'::set_json_domain", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] INTERSECT SELECT '{\"{}\"}'::JSON[]", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca INTERSECT SELECT v FROM scb", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "36", Constraint: ""}, "prepare"},
			{"SELECT TRUE INTERSECT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types boolean and integer cannot be matched", Position: "30", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT INTERSECT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types text and integer cannot be matched", Position: "35", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON INTERSECT DISTINCT SELECT '{}'::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON INTERSECT DISTINCT SELECT NULL::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj INTERSECT DISTINCT SELECT v FROM sj", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain INTERSECT DISTINCT SELECT '{}'::set_json_domain", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] INTERSECT DISTINCT SELECT '{\"{}\"}'::JSON[]", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca INTERSECT DISTINCT SELECT v FROM scb", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "45", Constraint: ""}, "prepare"},
			{"SELECT TRUE INTERSECT DISTINCT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types boolean and integer cannot be matched", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT INTERSECT DISTINCT SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types text and integer cannot be matched", Position: "44", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON INTERSECT ALL SELECT '{}'::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
		} {
			s := referenceErrorDiagnostic(tc.q, tc.want)
			s.selectRows = true
			querySetOpsDiagnosticRoutes(t, p, b, s, tc.preparedStage)
		}
	})
}
