package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreFeatureSmoke(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE more_smoke(k integer,a int4range,b int8range,n numrange,m int4multirange)", 0))
		run(fixture.Exec("INSERT INTO more_smoke VALUES (1,'[1,4]','[1,4]','(1.00,2.000]','{[8,9),empty,[1,3),[2,4),[9,10)}'),(2,'empty','empty','empty','{}'),(3,NULL,NULL,NULL,NULL)", 3))
		moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS a,multirange(b) AS b,multirange(n) AS n FROM more_smoke WHERE k=1", []string{"a", "b", "n"}, []string{"INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"}, [][]any{{"{[1,5)}", "{[1,5)}", "{(1.00,2.000]}"}}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT r,ord FROM more_smoke,unnest(m) WITH ORDINALITY AS u(r,ord) WHERE k=1", []string{"r", "ord"}, []string{"INT4RANGE", "INT8"}, [][]any{{"[1,4)", int64(1)}, {"[8,10)", int64(2)}}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS m FROM more_smoke WHERE k=2 OR k=3 ORDER BY k", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{}"}, {nil}}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_smoke WHERE k=2 OR k=3", []string{"r"}, []string{"INT4RANGE"}, [][]any{}, nil))
		q := "SELECT /*é*/ unnest(1)"
		s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42883", "function unnest(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "unnest", "", ""))
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		moreStored(t, p, b)
	})
}
