package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Complete logical diagnostics from pinned18.0 (root artifact8).
// Unspecified fields are asserted empty by referenceAssertDiagnostic.
func TestReferenceQuerySetOpsBoundaryDiagnostics(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		querySetOpsSeed(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sj(v JSON)", 0),
			fixture.Exec("CREATE TABLE sca(v TEXT COLLATE \"C\")", 0),
			fixture.Exec("CREATE TABLE scb(v TEXT COLLATE \"POSIX\")", 0),
			fixture.Exec("CREATE TABLE sm(a NUMERIC(6,2),b NUMERIC(7,3),c VARCHAR(5),d VARCHAR(7),n INT NOT NULL)", 0),
			fixture.Exec("CREATE DOMAIN set_json_domain AS JSON", 0),
			fixture.Exec("CREATE TYPE set_enum AS ENUM('a','b')", 0),
			fixture.Exec("CREATE TYPE set_enum_other AS ENUM('a','b')", 0),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, tc := range []struct {
			q             string
			want          fixture.Diagnostic
			preparedStage string
		}{
			{"SELECT NULL::JSON INTERSECT ALL SELECT NULL::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj INTERSECT ALL SELECT v FROM sj", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain INTERSECT ALL SELECT '{}'::set_json_domain", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] INTERSECT ALL SELECT '{\"{}\"}'::JSON[]", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca INTERSECT ALL SELECT v FROM scb", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "40", Constraint: ""}, "prepare"},
			{"SELECT TRUE INTERSECT ALL SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types boolean and integer cannot be matched", Position: "34", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT INTERSECT ALL SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "INTERSECT types text and integer cannot be matched", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON EXCEPT SELECT '{}'::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON EXCEPT SELECT NULL::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj EXCEPT SELECT v FROM sj", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain EXCEPT SELECT '{}'::set_json_domain", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] EXCEPT SELECT '{\"{}\"}'::JSON[]", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca EXCEPT SELECT v FROM scb", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "33", Constraint: ""}, "prepare"},
			{"SELECT TRUE EXCEPT SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types boolean and integer cannot be matched", Position: "27", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT EXCEPT SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types text and integer cannot be matched", Position: "32", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON EXCEPT DISTINCT SELECT '{}'::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON EXCEPT DISTINCT SELECT NULL::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj EXCEPT DISTINCT SELECT v FROM sj", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain EXCEPT DISTINCT SELECT '{}'::set_json_domain", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] EXCEPT DISTINCT SELECT '{\"{}\"}'::JSON[]", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca EXCEPT DISTINCT SELECT v FROM scb", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "42", Constraint: ""}, "prepare"},
			{"SELECT TRUE EXCEPT DISTINCT SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types boolean and integer cannot be matched", Position: "36", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT EXCEPT DISTINCT SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types text and integer cannot be matched", Position: "41", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON EXCEPT ALL SELECT '{}'::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT NULL::JSON EXCEPT ALL SELECT NULL::JSON", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sj EXCEPT ALL SELECT v FROM sj", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{}'::set_json_domain EXCEPT ALL SELECT '{}'::set_json_domain", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type set_json_domain", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '{\"{}\"}'::JSON[] EXCEPT ALL SELECT '{\"{}\"}'::JSON[]", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json[]", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca EXCEPT ALL SELECT v FROM scb", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between implicit collations \"C\" and \"POSIX\"", Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "37", Constraint: ""}, "prepare"},
			{"SELECT TRUE EXCEPT ALL SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types boolean and integer cannot be matched", Position: "31", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT EXCEPT ALL SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "EXCEPT types text and integer cannot be matched", Position: "36", Constraint: ""}, "prepare"},
		} {
			s := fixture.ErrorDiagnostic(tc.q, tc.want)
			s.SelectRows = true
			querySetOpsDiagnosticRoutes(t, p, b, s, tc.preparedStage)
		}
	})
}
