package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowExcludeViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE DOMAIN ex_value AS INT", 0),
		fixture.Exec("CREATE TABLE ex_argument(v INT)", 0),
		fixture.Exec("INSERT INTO ex_argument VALUES(1)", 1),
		fixture.Exec("CREATE VIEW ex_named AS SELECT id,count(*) OVER w AS n,sum(v) OVER w AS s FROM ex_t WINDOW w AS(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES)", 0),
		fixture.Exec("CREATE VIEW ex_current AS SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) AS n,sum(v) OVER(ORDER BY k RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) AS s FROM ex_t", 0),
		fixture.Exec("CREATE VIEW ex_group AS SELECT id,count(*) OVER w AS n,sum(v) OVER w AS s FROM ex_t WINDOW w AS(ORDER BY k GROUPS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE GROUP)", 0),
		fixture.Exec("CREATE VIEW ex_arguments AS SELECT id,sum(v+'1'::ex_value) OVER w AS literal,sum(v+(SELECT v FROM ex_argument)) OVER w AS sub FROM ex_t WINDOW w AS(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES)", 0),
		fixture.Exec("ALTER TABLE ex_t RENAME COLUMN v TO renamed", 0),
		fixture.Exec("ALTER TABLE ex_argument RENAME COLUMN v TO renamed", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	var steps []fixture.Step
	for _, tc := range []struct {
		name string
		n    []int64
		s    []any
	}{
		{"ex_named", []int64{2, 2, 3}, []any{int64(40), int64(50), int64(60)}},
		{"ex_current", []int64{2, 2, 2}, []any{int64(50), int64(40), int64(30)}},
		{"ex_group", []int64{1, 1, 2}, []any{int64(30), int64(30), int64(30)}},
	} {
		s := queryWindowExcludeResult("SELECT id,n,s FROM "+tc.name+" ORDER BY id", tc.n, tc.s)
		// Stored view columns are known nullable, including count outputs;
		// direct window expressions retain their own nullability contract.
		s.CheckMetadata = queryWindowExcludeMetadata(b.PG, true, true, true)
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, queryWindowReviewReplay(t, p, b, tc.name, s)...)
	}
	s := fixture.Select("SELECT id,literal,sub FROM ex_arguments ORDER BY id", []string{"id", "literal", "sub"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(42), int64(42)}, {int64(2), int64(52), int64(52)}, {int64(3), int64(63), int64(63)}})
	queryWindowRoutes(t, p, b, s)
	steps = append(steps, queryWindowReviewReplay(t, p, b, "ex_arguments", s)...)
	if !b.PG {
		queryWindowRangeControls(t, p, steps)
	}
}

func TestReferenceQueryWindowExcludeViews(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludeViews(t, p, b)
	})
}
