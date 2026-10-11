package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticContextualDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE d(f BOOL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
		}
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"INSERT INTO d VALUES(1+2)", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(2147483648+2)", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(/*é*/(1+2))", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "28", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(- (2147483648+2))", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"SELECT i+1 FROM t WHERE i+1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "25"}},
			{"SELECT i+1 FROM t WHERE (i+1)", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "26"}},
			{"SELECT /*é*/ i+1 FROM t WHERE i+1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "31"}},
			{"SELECT i FROM t WHERE +i", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "23"}},
		} {
			step := fixture.ImplicitError(tc.q, tc.d)
			step.SelectRows = !strings.HasPrefix(tc.q, "INSERT")
			step.ErrorStage = "execute"
			p.Step(t, step)
			step.ErrorStage = "prepare"
			p.Step(t, fixture.Prepared(step))
		}
	}
	p.Step(t, fixture.Select("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, nil))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, nil)))
}

func TestReferenceArithmeticCombinedOperatorBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	step := fixture.ImplicitError("SELECT i%-1 FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer %- integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	step.SelectRows = true
	step.ErrorStage = "execute"
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
	failed := fixture.ImplicitError("INSERT INTO t VALUES(1),(7%-1)", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer %- integer", Position: "27", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	failed.ErrorStage = "execute"
	p.Step(t, failed)
	failed.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(failed))
	p.Step(t, fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})))
	p.Step(t, fixture.Select("SELECT i% -1 FROM t", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(0)}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT i% -1 FROM t", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(0)}})))
}

func TestReferenceArithmeticTruthTestContextualDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,7),(NULL,NULL)", 2))
		}
		for _, predicate := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
			for _, operand := range []struct{ column, diagnostic string }{{"i", "integer"}, {"b", "bigint"}} {
				for _, tc := range []struct{ prefix, suffix, position string }{
					{"SELECT " + operand.column + "+1 ", " FROM t", "8"},
					{"SELECT (" + operand.column + "+1) ", " FROM t", "9"},
					{"SELECT /*é*/ " + operand.column + "+1 ", " FROM t", "14"},
					{"SELECT /*é*/ (" + operand.column + "+1) ", " FROM t", "15"},
					{"SELECT " + operand.column + " FROM t WHERE " + operand.column + "+1 ", "", "23"},
					{"SELECT " + operand.column + " FROM t WHERE (" + operand.column + "+1) ", "", "24"},
					{"SELECT " + operand.column + " FROM t WHERE /*é*/ " + operand.column + "+1 ", "", "29"},
					{"SELECT " + operand.column + " FROM t WHERE /*é*/ (" + operand.column + "+1) ", "", "30"},
					{"SELECT /*é*/ " + operand.column + " FROM t WHERE " + operand.column + "+1 ", "", "29"},
					{"SELECT /*é*/ " + operand.column + " FROM t WHERE (" + operand.column + "+1) ", "", "30"},
				} {
					step := fixture.ImplicitError(tc.prefix+predicate+tc.suffix, fixture.Diagnostic{Code: "42804", Message: "argument of " + predicate + " must be type boolean, not type " + operand.diagnostic, Position: tc.position})
					step.SelectRows = true
					step.ErrorStage = "execute"
					p.Step(t, step)
					step.ErrorStage = "prepare"
					p.Step(t, fixture.Prepared(step))
				}
			}
		}
	}
}
