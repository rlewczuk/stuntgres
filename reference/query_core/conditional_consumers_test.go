package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalConsumerAtomicity(t *testing.T) {
	p := conditionalReferenceSetup(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE out(v INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO out VALUES(COALESCE(NULL,1)),(NULLIF(2,3)),(GREATEST(NULL,4)),(LEAST(NULL,5)),(CASE WHEN FALSE THEN 1/0 ELSE 6 END)", 5))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO out SELECT CASE WHEN b THEN 10 ELSE 20 END FROM t", 3))
	out := fixture.Select("SELECT v FROM out ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(10)}, {int64(20)}, {int64(20)}})
	fixture.ScalarStep(t, p, out)
	for _, q := range []string{"UPDATE t SET v=CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END", "DELETE FROM t WHERE CASE WHEN id=1 THEN TRUE ELSE 10/(id-2)>0 END", "INSERT INTO out SELECT CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END FROM t"} {
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Select("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}, {nil}}))
		fixture.ScalarStep(t, p, out)
	}
}
