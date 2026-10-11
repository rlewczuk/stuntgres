package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAggregateDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0))
	for _, tc := range []struct{ q, code, message, position string }{
		{"SELECT k,v,count(*) FROM t GROUP BY k", "42803", `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, "10"},
		{"SELECT k FROM t GROUP BY k HAVING v>0", "42803", `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, "35"},
		{"SELECT k FROM t GROUP BY k ORDER BY v", "42803", `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, "37"},
		{"SELECT count(*) FROM t WHERE count(*)>0", "42803", "aggregate functions are not allowed in WHERE", "30"},
		{"SELECT k FROM t GROUP BY count(*)", "42803", "aggregate functions are not allowed in GROUP BY", "26"},
		{"SELECT count(*) FROM t GROUP BY 1", "42803", "aggregate functions are not allowed in GROUP BY", "8"},
		{"SELECT missing,count(*) FROM t", "42703", `column "missing" does not exist`, "8"},
		{"SELECT k FROM t GROUP BY missing", "42703", `column "missing" does not exist`, "26"},
		{"SELECT k FROM t GROUP BY k HAVING missing", "42703", `column "missing" does not exist`, "35"},
		{"SELECT count(*) FROM t HAVING 1", "42804", "argument of HAVING must be type boolean, not type integer", "31"},
		{"SELECT k FROM t GROUP BY 0", "42P10", "GROUP BY position 0 is not in select list", "26"},
		{"SELECT k FROM t GROUP BY -1", "42P10", "GROUP BY position -1 is not in select list", "26"},
		{"SELECT k FROM t GROUP BY 2", "42P10", "GROUP BY position 2 is not in select list", "26"},
		{"SELECT k FROM t GROUP BY 2147483648", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY -2147483648", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY 1.5", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY 'x'", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY NULL", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY TRUE", "42601", "non-integer constant in GROUP BY", "26"},
		{"SELECT k FROM t GROUP BY", "42601", "syntax error at end of input", "25"},
		{"SELECT k FROM t HAVING", "42601", "syntax error at end of input", "23"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			step.SelectRows = true
			step.ErrorStage = "execute"
			p.Step(t, step)
			step.ErrorStage = "prepare"
			p.Step(t, fixture.Prepared(step))
		})
	}
}
