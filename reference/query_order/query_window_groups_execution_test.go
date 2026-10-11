package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowGroupsScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func queryWindowGroupsSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE g(id INT NOT NULL,k INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO g VALUES(1,1),(2,1),(3,2),(4,3),(5,3)", 5))
}

func queryWindowGroupsResult(frame string, values ...int64) fixture.Step {
	return queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k GROUPS "+frame+") AS n FROM g ORDER BY id", values...)
}

func TestReferenceQueryWindowGroupsExecution(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		for _, tc := range []struct {
			spec   string
			values []int64
		}{
			{"ORDER BY k GROUPS 1 PRECEDING", []int64{2, 2, 3, 3, 3}},
			{"ORDER BY k DESC GROUPS 1 PRECEDING", []int64{3, 3, 3, 2, 2}},
			{"ORDER BY k,id GROUPS 1 PRECEDING", []int64{1, 2, 2, 2, 2}},
			{"ORDER BY 1 GROUPS 1 PRECEDING", []int64{5, 5, 5, 5, 5}},
			{"ORDER BY 1 GROUPS BETWEEN 1 FOLLOWING AND 1 FOLLOWING", []int64{0, 0, 0, 0, 0}},
			{"PARTITION BY k ORDER BY id GROUPS 1 PRECEDING", []int64{1, 2, 1, 1, 2}},
		} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER("+tc.spec+") AS n FROM g ORDER BY id", tc.values...))
		}
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO g VALUES(6,NULL),(7,NULL)", 2))
		for _, tc := range []struct {
			spec   string
			values []int64
		}{
			{"ASC NULLS LAST", []int64{2, 2, 3, 3, 3, 4, 4}},
			{"ASC NULLS FIRST", []int64{4, 4, 3, 3, 3, 2, 2}},
			{"DESC NULLS LAST", []int64{3, 3, 3, 2, 2, 4, 4}},
			{"DESC NULLS FIRST", []int64{3, 3, 3, 4, 4, 2, 2}},
		} {
			queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k "+tc.spec+" GROUPS 1 PRECEDING) AS n FROM g ORDER BY id", tc.values...))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE gaps(id INT,k INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO gaps VALUES(1,1),(2,10),(3,100)", 3))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) AS n,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS r FROM gaps ORDER BY id", []string{"id", "n", "r"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(1)}, {int64(3), int64(2), int64(1)}}))
	})
	for _, typ := range []string{"TEXT", "BOOL", "groups_enum"} {
		t.Run(typ, func(t *testing.T) {
			queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				queryWindowRun(t, p, b, fixture.Exec("CREATE TYPE groups_enum AS ENUM('a','b','c')", 0))
				queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE carriers(id INT,k "+typ+")", 0))
				values := "(1,'a'),(2,'a'),(3,'b'),(4,'c'),(5,'c')"
				want := []int64{2, 2, 3, 3, 3}
				if typ == "BOOL" {
					values = "(1,FALSE),(2,FALSE),(3,TRUE),(4,TRUE),(5,TRUE)"
					want = []int64{2, 2, 5, 5, 5}
				}
				queryWindowRun(t, p, b, fixture.Exec("INSERT INTO carriers VALUES"+values, 5))
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) AS n FROM carriers ORDER BY id", want...))
			})
		})
	}
}

func TestReferenceQueryWindowGroupsBoundaries(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		for _, tc := range []struct {
			frame  string
			values []int64
		}{
			{"1 PRECEDING", []int64{2, 2, 3, 3, 3}},
			{"CURRENT ROW", []int64{2, 2, 1, 2, 2}},
			{"0 PRECEDING", []int64{2, 2, 1, 2, 2}},
			{"BETWEEN 0 PRECEDING AND 0 FOLLOWING", []int64{2, 2, 1, 2, 2}},
			{"BETWEEN 1 PRECEDING AND 1 PRECEDING", []int64{0, 0, 2, 1, 1}},
			{"BETWEEN 1 FOLLOWING AND 1 FOLLOWING", []int64{1, 1, 2, 0, 0}},
			{"BETWEEN CURRENT ROW AND 1 FOLLOWING", []int64{3, 3, 3, 2, 2}},
			{"BETWEEN 1 PRECEDING AND 1 FOLLOWING", []int64{3, 3, 5, 3, 3}},
			{"BETWEEN 2 PRECEDING AND 1 PRECEDING", []int64{0, 0, 2, 3, 3}},
			{"BETWEEN 1 PRECEDING AND 2 PRECEDING", []int64{0, 0, 0, 0, 0}},
			{"BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING", []int64{0, 0, 2, 3, 3}},
			{"BETWEEN 1 FOLLOWING AND UNBOUNDED FOLLOWING", []int64{3, 3, 2, 0, 0}},
			{"BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING", []int64{5, 5, 5, 5, 5}},
			{"9223372036854775807 PRECEDING", []int64{2, 2, 3, 5, 5}},
			{"BETWEEN CURRENT ROW AND 9223372036854775807 FOLLOWING", []int64{5, 5, 3, 2, 2}},
			{"BETWEEN 9223372036854775807 FOLLOWING AND UNBOUNDED FOLLOWING", []int64{0, 0, 0, 0, 0}},
		} {
			t.Run(tc.frame, func(t *testing.T) { queryWindowRoutes(t, p, b, queryWindowGroupsResult(tc.frame, tc.values...)) })
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE empty_g(id INT,k INT)", 0))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) AS n FROM empty_g ORDER BY id"))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT count(*) OVER(ORDER BY 1 GROUPS 1 PRECEDING)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT count(*) OVER(ORDER BY 1 GROUPS 1 PRECEDING) WHERE FALSE", []string{"count"}, []string{"INT8"}, nil))
	})
}
