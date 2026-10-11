package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualWindowIdentity(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE l(x INT)`, 0))
		run(fixture.Exec(`INSERT INTO l VALUES(1),(2),(3)`, 3))
		run(fixture.Exec(`CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL,n INT GENERATED ALWAYS AS(coalesce(a,9)) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(NULL)`, 1))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query string
				rows  [][]any
			}{
				{`SELECT l.x,g.c,row_number() OVER () AS rn FROM l LEFT JOIN g ON l.x=1 WHERE l.x=2`, [][]any{{int64(2), nil, int64(1)}}},
				// DESC permutes the window adapter's input. Outer ORDER alone
				// determines presentation; each qualified value keeps its row.
				{`SELECT l.x,g.c,row_number() OVER (ORDER BY l.x DESC) AS rn FROM l LEFT JOIN g ON l.x=1 ORDER BY l.x`, [][]any{{int64(1), int64(1), int64(3)}, {int64(2), nil, int64(2)}, {int64(3), nil, int64(1)}}},
				{`SELECT l.x,g.c,row_number() OVER (PARTITION BY g.c ORDER BY l.x) AS rn FROM l LEFT JOIN g ON l.x=1 ORDER BY l.x`, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), nil, int64(1)}, {int64(3), nil, int64(2)}}},
				{`SELECT l.x,g.c,row_number() OVER (ORDER BY g.n NULLS FIRST,l.x) AS rn FROM l LEFT JOIN g ON l.x=1 ORDER BY l.x`, [][]any{{int64(1), int64(1), int64(3)}, {int64(2), nil, int64(1)}, {int64(3), nil, int64(2)}}},
			} {
				s := fixture.Select(tc.query, []string{"x", "c", "rn"}, []string{"INT4", "INT4", "INT8"}, tc.rows)
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
