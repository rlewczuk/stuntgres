package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticComputedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
	for _, q := range []string{"SELECT i+1,b*2 FROM t", "SELECT i+1,b*2 FROM t WHERE FALSE"} {
		want := [][]any{{nil, nil}}
		if q == "SELECT i+1,b*2 FROM t WHERE FALSE" {
			want = nil
		}
		step := fixture.Select(q, []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, want)
		referenceArithmeticMetadata(t, p, step)
	}
	step := fixture.Select("SELECT 7% -2,-7% -2 FROM t", []string{"?column?", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-1)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	// database/sql and the public driver expose no physical-origin field. Native
	// bound result-schema origin absence is asserted in the engine fixture, not
	// approximated from names or invented as a PostgreSQL metadata API.
}
