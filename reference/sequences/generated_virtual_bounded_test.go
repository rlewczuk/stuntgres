package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualBoundedProjection(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE l(x INT)`, 0))
		run(fixture.Exec(`INSERT INTO l VALUES(1)`, 1))
		run(fixture.Exec(`CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL,n INT GENERATED ALWAYS AS(coalesce(a,9)) VIRTUAL,z INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
		// LIMIT without sorting schedules compound expressions before final
		// projection. Absence must be preserved at that earlier evaluation too.
		for _, present := range []bool{false, true} {
			if present {
				run(fixture.Exec(`INSERT INTO g(a) VALUES(NULL)`, 1))
			}
			want := [][]any{{nil, nil, nil, true}}
			if present {
				want = [][]any{{int64(2), int64(10), nil, false}}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(`SELECT g.c+1 AS c,g.n+1 AS n,g.z+1 AS z,g.c IS NULL AS missing FROM l LEFT JOIN g ON TRUE LIMIT 1`, []string{"c", "n", "z", "missing"}, []string{"INT4", "INT4", "INT4", "BOOL"}, want)
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
