package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceQueryWindowOffsetDiagnostics(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, fn := range []string{"lead", "lag"} {
			for i, tc := range []struct{ sql, code, message, marker, hint string }{
				{"SELECT F(1)", "42809", "window function F requires an OVER clause", "F", ""},
				{"SELECT pg_catalog.F(1)", "42809", "window function pg_catalog.F requires an OVER clause", "pg_catalog", ""},
				{"SELECT F(NULL) OVER()", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
				{"SELECT F(NULL,1) OVER()", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
				{"SELECT F('x') OVER()", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
				{"SELECT F() OVER()", "42883", "function F() does not exist", "F", hint},
				{"SELECT F(1,1,1,1) OVER()", "42883", "function F(integer, integer, integer, integer) does not exist", "F", hint},
				{"SELECT F(1,1::bigint) OVER()", "42883", "function F(integer, bigint) does not exist", "F", hint},
				{"SELECT F(1,1::numeric) OVER()", "42883", "function F(integer, numeric) does not exist", "F", hint},
				{"SELECT F(1,'1'::text) OVER()", "42883", "function F(integer, text) does not exist", "F", hint},
				{"SELECT F(1,TRUE) OVER()", "42883", "function F(integer, boolean) does not exist", "F", hint},
				{"SELECT F(1,0,'x'::text) OVER()", "42883", "function F(integer, integer, text) does not exist", "F", hint},
				{"SELECT F('x'::text,0,1) OVER()", "42883", "function F(text, integer, integer) does not exist", "F", hint},
				{"SELECT F(1,0,'bad') OVER()", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", ""},
				{"SELECT F(1,'bad') OVER()", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", ""},
				{"SELECT F(1,'2147483648') OVER()", "22003", `value "2147483648" is out of range for type integer`, "'2147483648'", ""},
				{"SELECT F(1,missing,0,0) OVER()", "42703", `column "missing" does not exist`, "missing", ""},
				{"SELECT 1 WHERE F(1,0,missing) OVER()>0", "42703", `column "missing" does not exist`, "missing", ""},
				{"SELECT 1 WHERE F(1,TRUE) OVER()>0", "42883", "function F(integer, boolean) does not exist", "F", hint},
				{"SELECT 1 WHERE F(1) OVER()>0", "42P20", "window functions are not allowed in WHERE", "F", ""},
				{"SELECT F(lag(1) OVER()) OVER()", "42P20", "window function calls cannot be nested", "lag(1)", ""},
				{"SELECT sum(F(1) OVER())", "42803", "aggregate function calls cannot contain window function calls", "F", ""},
				{"SELECT F(1) OVER missing", "42704", `window "missing" does not exist`, "missing", ""},
				{"SELECT F(1) FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", "F", ""},
				{"SELECT F(1) FILTER(WHERE missing) OVER()", "42703", `column "missing" does not exist`, "missing", ""},
				{"SELECT F(DISTINCT 1) OVER()", "0A000", "DISTINCT is not implemented for window functions", "F", ""},
				{"SELECT F(1 ORDER BY missing) OVER()", "0A000", "aggregate ORDER BY is not implemented for window functions", "F", ""},
				{"SELECT F(generate_series(1,2)) OVER()", "0A000", "window function calls cannot contain set-returning function calls", "generate_series", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT F(1) OVER(ROWS UNBOUNDED FOLLOWING)", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", "UNBOUNDED", ""},
				{"SELECT 1 GROUP BY F(1) OVER()", "42P20", "window functions are not allowed in GROUP BY", "F", ""},
				{"SELECT 1 HAVING F(1) OVER()>0", "42P20", "window functions are not allowed in HAVING", "F", ""},
				{"SELECT 1 LIMIT F(1) OVER()", "42P20", "window functions are not allowed in LIMIT", "F", ""},
				{"SELECT F(1) OVER(ORDER BY lag(1) OVER())", "42P20", "window functions are not allowed in window definitions", "lag(1)", ""},
			} {
				t.Run(fmt.Sprintf("%s/%d", fn, i), func(t *testing.T) {
					q := strings.ReplaceAll(tc.sql, "F(", fn+"(")
					marker := tc.marker
					if marker == "F" {
						marker = fn
					}
					message := strings.ReplaceAll(strings.ReplaceAll(tc.message, "function F", "function "+fn), "pg_catalog.F", "pg_catalog."+fn)
					for _, prefix := range []string{"", "/*é*/ "} {
						pos := 0
						if marker != "" {
							at := strings.LastIndex(q, marker)
							if at < 0 {
								t.Fatal("missing caret marker", marker, q)
							}
							pos = utf8.RuneCountInString(prefix+q[:at]) + 1
						}
						queryWindowValueDiagnosticRoutes(t, p, b, prefix+q, fixture.Diagnostic{Code: tc.code, Message: message, Position: fixture.Position(pos), Hint: tc.hint}, "prepare")
					}
				})
			}
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wo_diag(id INT,v INT)", 0))
		for _, tc := range []struct{ q, message, marker string }{
			{"SELECT lead(sum(v),0,id) OVER() FROM wo_diag", `column "wo_diag.id" must appear in the GROUP BY clause or be used in an aggregate function`, "id"},
			{"INSERT INTO wo_diag VALUES(1,2) RETURNING lag(id) OVER()", "window functions are not allowed in RETURNING", "lag"},
		} {
			queryWindowValueDiagnosticRoutes(t, p, b, tc.q, fixture.Diagnostic{Code: map[bool]string{true: "42803", false: "42P20"}[strings.HasPrefix(tc.q, "SELECT")], Message: tc.message, Position: fixture.Position(strings.Index(tc.q, tc.marker) + 1)}, "prepare")
		}
		for _, tc := range []struct{ q, code, message, marker string }{
			{"CREATE TABLE wo_check(id INT CHECK(lead(id) OVER()>0))", "42P20", "window functions are not allowed in check constraints", "lead"},
			{"CREATE TABLE wo_default(id INT DEFAULT lag(1) OVER())", "42P20", "window functions are not allowed in DEFAULT expressions", "lag"},
			{"CREATE TABLE wo_generated(id INT,v INT GENERATED ALWAYS AS(lead(id) OVER()) STORED)", "42P20", "window functions are not allowed in column generation expressions", "lead"},
			{"CREATE INDEX wo_index ON wo_diag((lag(id) OVER()))", "42P20", "window functions are not allowed in index expressions", "lag"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(strings.Index(tc.q, tc.marker) + 1)})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
			}
		}
		for _, tc := range []struct {
			arg           any
			code, message string
		}{{"bad", "22P02", `invalid input syntax for type integer: "bad"`}, {"2147483648", "22003", `value "2147483648" is out of range for type integer`}} {
			// Bind conversion is a protocol-stage error: unlike literal coercion it has no SQL caret.
			for _, prepared := range []bool{false, true} {
				d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}
				s := fixture.ImplicitError("SELECT lead(1,$1,9) OVER()", d, tc.arg)
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
			}
		}
	})
}
