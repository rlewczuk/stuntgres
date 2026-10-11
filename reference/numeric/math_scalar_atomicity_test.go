package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarDomainAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE source(id INT,s SMALLINT,b BIGINT,n NUMERIC)", 0))
	p.Step(t, fixture.Exec("INSERT INTO source VALUES(1,-5,5,7.00),(2,-32768,-1,8.00)", 2))
	p.Step(t, fixture.Exec("CREATE TABLE copied(n NUMERIC)", 0))
	for _, tc := range []struct{ query, message string }{
		{"UPDATE source SET n=abs(s)", "smallint out of range"},
		{"INSERT INTO copied SELECT abs(s) FROM source", "smallint out of range"},
		{"UPDATE source SET n=factorial(b)", "factorial of a negative number is undefined"},
		{"INSERT INTO copied SELECT factorial(b) FROM source", "factorial of a negative number is undefined"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22003", Message: tc.message})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
			p.Step(t, fixture.Select("SELECT n FROM source ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"7.00"}, {"8.00"}}))
			p.Step(t, fixture.Select("SELECT n FROM copied", []string{"n"}, []string{"NUMERIC"}, [][]any{}))
		}
	}
}
