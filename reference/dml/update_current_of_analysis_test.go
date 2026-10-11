package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func updateCurrentOfSyntaxAndAnalysis(t *testing.T) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(10)", 1))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, code, msg, token string }{
				{"UPDATE t SET v=1 WHERE CURRENT OF $1", "42601", `syntax error at or near "$1"`, "$1"},
				{`UPDATE t SET v=1 WHERE CURRENT OF "é" AND TRUE`, "42601", `syntax error at or near "AND"`, "AND"},
				{"UPDATE t SET v=1 WHERE CURRENT OF c OR TRUE", "42601", `syntax error at or near "OR"`, "OR"},
				{`UPDATE t SET v=1 WHERE "current" OF c`, "42601", `syntax error at or near "OF"`, "OF"},
				{`UPDATE t SET v=1 WHERE CURRENT "of" c`, "42601", `syntax error at or near ""of""`, `"of"`},
				{"UPDATE t SET v=1 WHERE CURRENT OF public.c", "42601", `syntax error at or near "."`, "."},
				{"UPDATE t SET v=1 WHERE CURRENT OF c junk", "42601", `syntax error at or near "junk"`, "junk"},
				{"UPDATE absent SET v=1 WHERE CURRENT OF c", "42P01", `relation "absent" does not exist`, "absent"},
				{"UPDATE t SET absent=1 WHERE CURRENT OF c", "42703", `column "absent" of relation "t" does not exist`, "absent"},
				{"UPDATE t SET v='bad' WHERE CURRENT OF c", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'"},
				{"UPDATE t SET v=TRUE WHERE CURRENT OF c", "42804", `column "v" is of type integer but expression is of type boolean`, "TRUE"},
			} {
				d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.msg, Position: updateFromPosition(tc.q, tc.token)}
				if tc.code == "42804" {
					d.Hint = "You will need to rewrite or cast the expression."
				}
				updateFromDiagnostic(t, p.Ctx, c, tc.q, d, prepared)
			}
		}
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	})
}
