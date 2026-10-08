package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceQueryOrderValuesNameHint(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE d(i INT)", 0))
	q := "INSERT INTO d VALUES(1) ORDER BY column1,colum1+0"
	step := referenceImplicitError(q, referenceDiagnostic{Code: "42703", Message: `column "colum1" does not exist`, Position: strconv.Itoa(strings.Index(q, "colum1") + 1), Hint: `Perhaps you meant to reference the column "*VALUES*.column1".`})
	step.errorStage = "execute"
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
	p.step(t, referenceSelect("SELECT i FROM d", []string{"i"}, []string{"INT4"}, nil))
}
