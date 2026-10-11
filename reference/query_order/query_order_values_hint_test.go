package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderValuesNameHint(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(i INT)", 0))
	q := "INSERT INTO d VALUES(1) ORDER BY column1,colum1+0"
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "colum1" does not exist`, Position: strconv.Itoa(strings.Index(q, "colum1") + 1), Hint: `Perhaps you meant to reference the column "*VALUES*.column1".`})
	step.ErrorStage = "execute"
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Select("SELECT i FROM d", []string{"i"}, []string{"INT4"}, nil))
}
