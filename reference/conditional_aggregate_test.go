//go:build reference

package reference_test

import "testing"

func TestReferenceConditionalAggregateComposition(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
	for _, step := range []referenceStep{
		referenceSelect("SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END,COALESCE(count(*),0),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM t", []string{"case", "coalesce", "nullif", "greatest", "least"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(3), int64(3), int64(3), int64(2)}}),
		referenceSelect("SELECT CASE WHEN count(*)=3 THEN 7 ELSE 0 END FROM t HAVING COALESCE(count(*),0)>0", []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}),
		referenceSelect("SELECT CASE WHEN v=1 THEN 10 ELSE 20 END,count(*) FROM t GROUP BY CASE WHEN v=1 THEN 10 ELSE 20 END", []string{"case", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(2)}, {int64(20), int64(1)}}),
		referenceSelect("SELECT COALESCE(v,0),count(*) FROM t GROUP BY COALESCE(v,0)", []string{"coalesce", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct {
		q          string
		diagnostic referenceDiagnostic
	}{
		{"SELECT CASE WHEN TRUE THEN count(*) ELSE v END FROM t", referenceDiagnostic{Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "42"}},
		{"SELECT COALESCE(count(*),0) FROM t GROUP BY 1", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "17"}},
		{"SELECT count(*) FROM t WHERE COALESCE(count(*),0)>0", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "38"}},
	} {
		step := referenceImplicitError(tc.q, tc.diagnostic)
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
}
