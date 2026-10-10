package reference_test

import "testing"

// Complete logical diagnostics from pinned18.0 (root artifact8).
// Unspecified fields are asserted empty by referenceAssertDiagnostic.
func TestReferenceQuerySetOpsDiagnostics(t *testing.T) {
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
			{"SELECT 1 UNION SELECT 2,3", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each UNION query must have the same number of columns", Position: "23", Constraint: ""}, "prepare"},
			{"SELECT 1 INTERSECT ALL SELECT 2,3", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each INTERSECT query must have the same number of columns", Position: "31", Constraint: ""}, "prepare"},
			{"SELECT 1 EXCEPT SELECT 2,3", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each EXCEPT query must have the same number of columns", Position: "24", Constraint: ""}, "prepare"},
			{"SELECT /*é*/ 1 UNION SELECT 2,3", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each UNION query must have the same number of columns", Position: "29", Constraint: ""}, "prepare"},
			{"SELECT 1::INT UNION SELECT 2::INT,3", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each UNION query must have the same number of columns", Position: "28", Constraint: ""}, "prepare"},
			{"SELECT * FROM sl UNION SELECT * FROM sm", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each UNION query must have the same number of columns", Position: "31", Constraint: ""}, "prepare"},
			{"SELECT FROM sl UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "each UNION query must have the same number of columns", Position: "29", Constraint: ""}, "prepare"},
			{"SELECT 1 UNION SELECT missing FROM sr", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "23", Constraint: ""}, "prepare"},
			{"SELECT 1 UNION SELECT 2,3 FROM missing_relation", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "relation \"missing_relation\" does not exist", Position: "32", Constraint: ""}, "prepare"},
			{"SELECT NULL UNION SELECT NULL UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "44", Constraint: ""}, "prepare"},
			{"SELECT TRUE UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types boolean and integer cannot be matched", Position: "26", Constraint: ""}, "prepare"},
			{"SELECT 1 UNION SELECT TRUE", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types integer and boolean cannot be matched", Position: "23", Constraint: ""}, "prepare"},
			{"SELECT 1 UNION SELECT 'bad'", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Position: "23", Constraint: ""}, "prepare"},
			{"SELECT 'bad' UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Position: "8", Constraint: ""}, "prepare"},
			{"SELECT '1'::TEXT UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "31", Constraint: ""}, "prepare"},
			{"VALUES(NULL) UNION SELECT 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "27", Constraint: ""}, "prepare"},
			{"SELECT 'a'::set_enum UNION SELECT 'a'::set_enum_other", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42846", Message: "UNION could not convert type set_enum_other to set_enum", Position: "35", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY x+1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "invalid UNION/INTERSECT/EXCEPT ORDER BY clause", Detail: "Only result column names can be used, not expressions or functions.", Hint: "Add the expression/function to every SELECT, or move the UNION into a FROM clause.", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY abs(x)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "invalid UNION/INTERSECT/EXCEPT ORDER BY clause", Detail: "Only result column names can be used, not expressions or functions.", Hint: "Add the expression/function to every SELECT, or move the UNION into a FROM clause.", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY missing", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY 2", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P10", Message: "ORDER BY position 2 is not in select list", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x,2 AS x UNION SELECT 3,4 ORDER BY x", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: "ORDER BY \"x\" is ambiguous", Position: "48", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x,1 AS x UNION SELECT 3,4 ORDER BY x", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: "ORDER BY \"x\" is ambiguous", Position: "48", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 AS y ORDER BY y", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"y\" does not exist", Detail: "There is a column named \"y\" in table \"*SELECT* 2\", but it cannot be referenced from this part of the query.", Position: "44", Constraint: ""}, "prepare"},
			{"SELECT v FROM sl UNION SELECT v FROM sr ORDER BY sl.v", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"sl\"", Position: "50", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY missing+1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "39", Constraint: ""}, "prepare"},
			{"SELECT 1 AS x UNION SELECT 2 ORDER BY abs(missing)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "43", Constraint: ""}, "prepare"},
			{"SELECT v FROM sca UNION ALL SELECT v FROM scb ORDER BY 1", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly.", Constraint: ""}, "execute"},
			{"SELECT 'a' COLLATE \"C\" UNION ALL SELECT 'b' COLLATE \"POSIX\"", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between explicit collations \"C\" and \"POSIX\"", Position: "45", Constraint: ""}, "prepare"},
			{"SELECT (SELECT 1 UNION ALL SELECT 2)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "more than one row returned by a subquery used as an expression", Constraint: ""}, "execute"},
			{"SELECT 1 UNION SELECT 2 nonsense suffix", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"suffix\"", Position: "34", Constraint: ""}, "prepare"},
			{"SELECT '{}'::JSON UNION SELECT '{}'::JSON", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type json", Position: "8", Constraint: ""}, "prepare"},
		} {
			s := referenceErrorDiagnostic(tc.q, tc.want)
			s.selectRows = true
			querySetOpsDiagnosticRoutes(t, p, b, s, tc.preparedStage)
		}
	})
}
