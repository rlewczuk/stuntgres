//go:build reference

package reference_test

import "testing"

func TestReferenceQueryOrderGroupedBoundaries(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b INT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(1,2),(1,3)", 2))
		}
		count := int64(0)
		if populated {
			count = 2
		}
		for _, step := range []referenceStep{
			referenceSelect("SELECT count(*) AS n FROM t ORDER BY count(*)", []string{"n"}, []string{"INT8"}, [][]any{{count}}),
			referenceSelect("SELECT count(*) AS x,count(*) AS x FROM t ORDER BY x", []string{"x", "x"}, []string{"INT8", "INT8"}, [][]any{{count, count}}),
		} {
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
		step := referenceImplicitError("SELECT a AS b,count(*) FROM t GROUP BY b", referenceDiagnostic{Code: "42803", Message: `column "t.a" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
}
