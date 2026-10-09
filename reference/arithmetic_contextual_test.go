package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceArithmeticContextualDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
	p.step(t, referenceExec("CREATE TABLE d(f BOOL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
		}
		for _, tc := range []struct {
			q string
			d referenceDiagnostic
		}{
			{"INSERT INTO d VALUES(1+2)", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(2147483648+2)", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(/*é*/(1+2))", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "28", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(- (2147483648+2))", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"SELECT i+1 FROM t WHERE i+1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "25"}},
			{"SELECT i+1 FROM t WHERE (i+1)", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "26"}},
			{"SELECT /*é*/ i+1 FROM t WHERE i+1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "31"}},
			{"SELECT i FROM t WHERE +i", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "23"}},
		} {
			step := referenceImplicitError(tc.q, tc.d)
			step.selectRows = !strings.HasPrefix(tc.q, "INSERT")
			step.errorStage = "execute"
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		}
	}
	p.step(t, referenceSelect("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, nil))
	p.step(t, referencePrepared(referenceSelect("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, nil)))
}

func TestReferenceArithmeticCombinedOperatorBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	step := referenceImplicitError("SELECT i%-1 FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer %- integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	step.selectRows = true
	step.errorStage = "execute"
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
	failed := referenceImplicitError("INSERT INTO t VALUES(1),(7%-1)", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer %- integer", Position: "27", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	failed.errorStage = "execute"
	p.step(t, failed)
	failed.errorStage = "prepare"
	p.step(t, referencePrepared(failed))
	p.step(t, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.step(t, referencePrepared(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})))
	p.step(t, referenceSelect("SELECT i% -1 FROM t", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(0)}}))
	p.step(t, referencePrepared(referenceSelect("SELECT i% -1 FROM t", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(0)}})))
}

func TestReferenceArithmeticTruthTestContextualDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT,b BIGINT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(7,7),(NULL,NULL)", 2))
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
					step := referenceImplicitError(tc.prefix+predicate+tc.suffix, referenceDiagnostic{Code: "42804", Message: "argument of " + predicate + " must be type boolean, not type " + operand.diagnostic, Position: tc.position})
					step.selectRows = true
					step.errorStage = "execute"
					p.step(t, step)
					step.errorStage = "prepare"
					p.step(t, referencePrepared(step))
				}
			}
		}
	}
}
