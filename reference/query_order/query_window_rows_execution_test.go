package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowRowsScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		body(p, backend)
	}
}

func queryWindowRowsSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowGroupsSeed(t, p, b)
}

func queryWindowRowsResult(frame string, values ...int64) fixture.Step {
	return queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k,id ROWS "+frame+") AS n FROM g ORDER BY id", values...)
}

func TestReferenceQueryWindowRowsExecution(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE rows_t(id INT,k INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO rows_t VALUES(1,1),(2,3),(3,5)", 3))
		for _, tc := range []struct {
			frame  string
			values []int64
		}{
			{"BETWEEN 1 PRECEDING AND CURRENT ROW", []int64{1, 2, 2}},
			{"1 PRECEDING", []int64{1, 2, 2}},
			{"CURRENT ROW", []int64{1, 1, 1}},
			{"0 PRECEDING", []int64{1, 1, 1}},
			{"BETWEEN 0 FOLLOWING AND 0 FOLLOWING", []int64{1, 1, 1}},
			{"UNBOUNDED PRECEDING", []int64{1, 2, 3}},
			{"BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING", []int64{3, 3, 3}},
			{"BETWEEN CURRENT ROW AND 1 FOLLOWING", []int64{2, 2, 1}},
			{"BETWEEN 1 FOLLOWING AND 1 FOLLOWING", []int64{1, 1, 0}},
			{"BETWEEN 1 PRECEDING AND 1 PRECEDING", []int64{0, 1, 1}},
			{"BETWEEN 2 PRECEDING AND 1 PRECEDING", []int64{0, 1, 2}},
			{"BETWEEN 1 PRECEDING AND 2 PRECEDING", []int64{0, 0, 0}},
			{"BETWEEN 2 FOLLOWING AND 1 FOLLOWING", []int64{0, 0, 0}},
			{"BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING", []int64{0, 1, 2}},
			{"BETWEEN 1 FOLLOWING AND UNBOUNDED FOLLOWING", []int64{2, 1, 0}},
			{"100 PRECEDING", []int64{1, 2, 3}},
			{"BETWEEN CURRENT ROW AND 100 FOLLOWING", []int64{3, 2, 1}},
			{"BETWEEN 100 FOLLOWING AND UNBOUNDED FOLLOWING", []int64{0, 0, 0}},
		} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY id ROWS "+tc.frame+") AS n FROM rows_t ORDER BY id", tc.values...))
		}
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY id DESC ROWS 1 PRECEDING) AS n FROM rows_t ORDER BY id", 2, 2, 1))
		s := fixture.Select("SELECT count(*) OVER(ROWS 1 PRECEDING) AS n FROM rows_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}})
		s.Bag = true
		queryWindowRoutes(t, p, b, s)
		for _, frame := range []string{"CURRENT ROW", "BETWEEN 1 FOLLOWING AND 1 FOLLOWING"} {
			n := int64(1)
			if frame != "CURRENT ROW" {
				n = 0
			}
			q := "SELECT count(*) OVER(ROWS " + frame + ") AS n"
			queryWindowRoutes(t, p, b, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			queryWindowRoutes(t, p, b, fixture.Select(q+" WHERE FALSE", []string{"n"}, []string{"INT8"}, nil))
		}
	})
}

func TestReferenceQueryWindowRowsPartitions(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(PARTITION BY p ORDER BY id ROWS 1 PRECEDING) AS n FROM t ORDER BY id", 1, 2, 2, 1, 2, 1, 2))
		for _, order := range []string{"k ASC NULLS FIRST,id", "k ASC NULLS LAST,id", "k DESC NULLS FIRST,id", "k DESC NULLS LAST,id"} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY "+order+" ROWS CURRENT ROW) AS n FROM t ORDER BY id", 1, 1, 1, 1, 1, 1, 1))
		}
		queryWindowRowsSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowRowsResult("1 PRECEDING", 1, 2, 2, 2, 2))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n,count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) AS g,count(*) OVER(ORDER BY k) AS r FROM g ORDER BY id", []string{"id", "n", "g", "r"}, []string{"INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(2), int64(2)}, {int64(2), int64(2), int64(2), int64(2)}, {int64(3), int64(2), int64(3), int64(3)}, {int64(4), int64(2), int64(3), int64(5)}, {int64(5), int64(2), int64(3), int64(5)}}))
		// Nonunique order: only shared-sort relationships are portable, not id ties.
		queryWindowRoutes(t, p, b, fixture.Select("SELECT rn,n FROM (SELECT row_number() OVER(ORDER BY k) AS rn,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n FROM g) AS tied ORDER BY rn", []string{"rn", "n"}, []string{"INT8", "INT8"}, queryWindowValues(1, 2, 2, 2, 2)))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT rn,n,g,r FROM (SELECT row_number() OVER(ORDER BY k) AS rn,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n,count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) AS g,count(*) OVER(ORDER BY k) AS r FROM g) AS shared ORDER BY rn", []string{"rn", "n", "g", "r"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(2), int64(2)}, {int64(2), int64(2), int64(2), int64(2)}, {int64(3), int64(2), int64(3), int64(3)}, {int64(4), int64(2), int64(3), int64(5)}, {int64(5), int64(2), int64(3), int64(5)}}))
	})
}
