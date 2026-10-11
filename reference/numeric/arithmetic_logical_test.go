//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Arithmetic syntax is admitted inside logical operands; a Boolean input must
// fail operator resolution independently of whether the table contains rows.
func TestReferenceArithmeticLogicalOperandDiagnostics(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a BOOL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(TRUE)", 1))
		}
		step := fixture.ImplicitError("SELECT a+1 AND a FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: boolean + integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))
	}
}
