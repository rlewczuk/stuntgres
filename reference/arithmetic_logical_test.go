//go:build reference

package reference_test

import "testing"

// Arithmetic syntax is admitted inside logical operands; a Boolean input must
// fail operator resolution independently of whether the table contains rows.
func TestReferenceArithmeticLogicalOperandDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a BOOL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(TRUE)", 1))
		}
		step := referenceImplicitError("SELECT a+1 AND a FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean + integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
}
