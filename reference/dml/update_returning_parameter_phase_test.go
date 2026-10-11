package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningParameterPhase(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,f BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,TRUE)", 1))
	for _, tc := range []struct {
		q string
		d fixture.Diagnostic
	}{
		{"UPDATE t SET a=$1,f=$1 RETURNING 1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}},
		{"UPDATE t SET a=$1,f=$1 RETURNING $1::int4", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "21"}},
	} {
		p.Step(t, fixture.ImplicitError(tc.q, tc.d, 2))
		queryError := fixture.ImplicitError(tc.q, tc.d, 2)
		queryError.SelectRows = true
		p.Step(t, queryError)
		p.Step(t, fixture.Prepared(fixture.ImplicitError(tc.q, tc.d, 2)))
	}
	p.Step(t, fixture.Select("SELECT a,f FROM t", []string{"a", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
}
