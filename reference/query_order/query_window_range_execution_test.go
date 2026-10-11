package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowRangeScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func queryWindowRangeSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE range_t(id INT,k INT)", 0),
		fixture.Exec("INSERT INTO range_t VALUES(1,1),(2,2),(3,2),(4,4),(5,5),(6,NULL),(7,NULL)", 7),
	} {
		queryWindowRun(t, p, b, s)
	}
}

func queryWindowRangeResult(q string, values ...int64) fixture.Step {
	return fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(values...))
}

// Runtime failures belong to Execute even on a fresh prepared route.
func queryWindowRangeRuntime(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		queryWindowRun(t, p, b, s)
	}
}

func TestReferenceQueryWindowRangeExecution(t *testing.T) {
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		for _, direction := range []string{"ASC", "DESC"} {
			for _, nulls := range []string{"FIRST", "LAST"} {
				for _, tc := range []struct {
					frame     string
					asc, desc []int64
				}{
					{"1 PRECEDING", []int64{1, 3, 3, 1, 2, 2, 2}, []int64{3, 2, 2, 2, 1, 2, 2}},
					{"BETWEEN 1 PRECEDING AND 1 FOLLOWING", []int64{3, 3, 3, 2, 2, 2, 2}, []int64{3, 3, 3, 2, 2, 2, 2}},
					{"BETWEEN 2 PRECEDING AND 1 PRECEDING", []int64{0, 1, 1, 2, 1, 2, 2}, []int64{2, 1, 1, 1, 0, 2, 2}},
					{"BETWEEN 1 FOLLOWING AND 2 FOLLOWING", []int64{2, 1, 1, 1, 0, 2, 2}, []int64{0, 1, 1, 2, 1, 2, 2}},
					{"BETWEEN 0 PRECEDING AND 0 FOLLOWING", []int64{1, 2, 2, 1, 1, 2, 2}, []int64{1, 2, 2, 1, 1, 2, 2}},
					{"BETWEEN 1 PRECEDING AND 2 PRECEDING", []int64{0, 0, 0, 0, 0, 2, 2}, []int64{0, 0, 0, 0, 0, 2, 2}},
					{"BETWEEN CURRENT ROW AND CURRENT ROW", []int64{1, 2, 2, 1, 1, 2, 2}, []int64{1, 2, 2, 1, 1, 2, 2}},
				} {
					v := tc.asc
					if direction == "DESC" {
						v = tc.desc
					}
					q := fmt.Sprintf("SELECT id,count(*) OVER(ORDER BY k %s NULLS %s RANGE %s) AS n FROM range_t ORDER BY id", direction, nulls, tc.frame)
					t.Run(direction+"_"+nulls+"_"+tc.frame, func(t *testing.T) { queryWindowRoutes(t, p, b, queryWindowRangeResult(q, v...)) })
				}
			}
		}
		for _, tc := range []struct {
			spec   string
			values []int64
		}{
			{"ORDER BY k NULLS LAST RANGE BETWEEN UNBOUNDED PRECEDING AND 1 FOLLOWING", []int64{3, 3, 3, 5, 5, 7, 7}},
			{"ORDER BY k NULLS FIRST RANGE BETWEEN UNBOUNDED PRECEDING AND 1 FOLLOWING", []int64{5, 5, 5, 7, 7, 2, 2}},
			{"RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING", []int64{7, 7, 7, 7, 7, 7, 7}},
			{"ORDER BY k RANGE UNBOUNDED PRECEDING", []int64{1, 3, 3, 4, 5, 7, 7}},
			{"ORDER BY k,k DESC NULLS FIRST RANGE 1 PRECEDING", []int64{1, 3, 3, 1, 2, 2, 2}},
		} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER("+tc.spec+") AS n FROM range_t ORDER BY id", tc.values...))
		}
		for _, offset := range []string{"'1'", "1::SMALLINT", "1::BIGINT", "(1+0)", "(SELECT 1)", "$1"} {
			s := queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE "+offset+" PRECEDING) AS n FROM range_t ORDER BY id", 1, 3, 3, 1, 2, 2, 2)
			if offset == "$1" {
				s.Args = []any{1}
			}
			queryWindowRoutes(t, p, b, s)
		}
		for _, over := range []string{"w", "(base RANGE 1 PRECEDING)"} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER "+over+" AS n FROM range_t WINDOW base AS(ORDER BY k),w AS(base RANGE 1 PRECEDING) ORDER BY id", 1, 3, 3, 1, 2, 2, 2))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE gaps(id INT,k INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO gaps VALUES(1,1),(2,3),(3,5)", 3))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM gaps ORDER BY id", 1, 1, 1))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n FROM gaps ORDER BY id", 1, 2, 2))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS a,count(*) OVER(ORDER BY k RANGE 3 PRECEDING) AS b,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS c FROM range_t ORDER BY id", []string{"id", "a", "b", "c"}, []string{"INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1), int64(1)}, {int64(2), int64(3), int64(3), int64(3)}, {int64(3), int64(3), int64(3), int64(3)}, {int64(4), int64(1), int64(4), int64(1)}, {int64(5), int64(2), int64(4), int64(2)}, {int64(6), int64(2), int64(2), int64(2)}, {int64(7), int64(2), int64(2), int64(2)}}))
		s := fixture.Select("SELECT row_number() OVER(ORDER BY k RANGE 1 PRECEDING) AS a,row_number() OVER(ORDER BY k RANGE 3 PRECEDING) AS b FROM range_t", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}, {int64(5), int64(5)}, {int64(6), int64(6)}, {int64(7), int64(7)}})
		s.Bag = true
		queryWindowRoutes(t, p, b, s)
	})
}

func TestReferenceQueryWindowRangeFeatureSmoke(t *testing.T) {
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM range_t ORDER BY id", 1, 3, 3, 1, 2, 2, 2))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k DESC RANGE 1 PRECEDING) AS n FROM range_t ORDER BY id", 3, 2, 2, 2, 1, 2, 2))
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE $1 PRECEDING) FROM range_t", "22004", "frame starting offset must not be null", nil)
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE 0 PRECEDING) AS n FROM range_t ORDER BY id", 1, 2, 2, 1, 1, 2, 2))
		queryWindowRangeSmokeContracts(t, p, b)
	})
}
