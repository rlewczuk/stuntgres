//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderGroupedBoundaries(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2),(1,3)", 2))
		}
		count := int64(0)
		if populated {
			count = 2
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT count(*) AS n FROM t ORDER BY count(*)", []string{"n"}, []string{"INT8"}, [][]any{{count}}),
			fixture.Select("SELECT count(*) AS x,count(*) AS x FROM t ORDER BY x", []string{"x", "x"}, []string{"INT8", "INT8"}, [][]any{{count, count}}),
		} {
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		}
		step := fixture.ImplicitError("SELECT a AS b,count(*) FROM t GROUP BY b", fixture.Diagnostic{Code: "42803", Message: `column "t.a" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))
	}
}
