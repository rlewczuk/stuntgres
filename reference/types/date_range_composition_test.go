package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDateRangeComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN span AS daterange",
			"CREATE TYPE envelope AS (a daterange,b span)",
			"CREATE TABLE r(k integer NOT NULL,v envelope)",
			"CREATE TABLE scalar(k integer NOT NULL,v daterange)",
		} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec(`INSERT INTO r VALUES (1,'("[2000-02-28,2000-03-01]","[2000-01-01,infinity]")'),(2,'(,)'),(3,NULL)`, 3))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 4, []byte(`("(2000-02-28,2000-03-01]","[2000-01-01,)")`))))
		check := rangeSelect(b, "SELECT k,(v).a AS a,(v).b AS b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "DATERANGE", "DATERANGE"}, [][]any{
			{int64(1), "[2000-02-28,2000-03-02)", "[2000-01-01,infinity]"},
			{int64(2), nil, nil}, {int64(3), nil, nil},
			{int64(4), "[2000-02-29,2000-03-02)", "[2000-01-01,)"},
		}, nil) // Computed composite fields do not inherit declaration nullability.
		for _, prepared := range []bool{false, true} {
			s := check
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("INSERT INTO scalar VALUES (1,'[2000-02-28,2000-03-01]'),(2,NULL)", 2))
		// Exactly one non-NULL datum makes ANY_VALUE deterministic without
		// assigning a portable traversal order to the aggregate.
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(b, "SELECT ANY_VALUE(v) AS v FROM scalar", []string{"v"}, []string{"DATERANGE"}, [][]any{{"[2000-02-28,2000-03-02)"}}, []bool{true})
			s.Prepared = prepared
			run(s)
		}
		rangeControlCycle(t, p, b, []fixture.Step{check}, [][]bool{nil}, nil)
	}
}
