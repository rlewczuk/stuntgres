package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func queryWindowRankProjection(over string) string {
	return "rank() OVER " + over + " AS r,dense_rank() OVER " + over + " AS d,percent_rank() OVER " + over + " AS pr,cume_dist() OVER " + over + " AS cd"
}

func queryWindowRankRows() [][]any {
	return [][]any{
		{int64(1), int64(1), int64(1), float64(0), float64(2) / 3},
		{int64(2), int64(1), int64(1), float64(0), float64(2) / 3},
		{int64(3), int64(3), int64(2), float64(1), float64(1)},
		{int64(4), int64(1), int64(1), float64(0), float64(.5)},
		{int64(5), int64(2), int64(2), float64(1), float64(1)},
		{int64(6), int64(1), int64(1), float64(0), float64(1)},
		{int64(7), int64(1), int64(1), float64(0), float64(1)},
	}
}

func queryWindowRankResult(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"id", "r", "d", "pr", "cd"}, []string{"INT4", "INT8", "INT8", "FLOAT8", "FLOAT8"}, rows, args...)
}

func queryWindowRankBasic(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	projection := queryWindowRankProjection("(PARTITION BY p ORDER BY k)")
	queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+projection+" FROM t ORDER BY id", queryWindowRankRows()))
	queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+queryWindowRankProjection("ordered")+" FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k) ORDER BY id", queryWindowRankRows()))
	qualified := strings.NewReplacer(
		"dense_rank()", "pg_catalog.dense_rank()",
		"percent_rank()", "pg_catalog.percent_rank()",
		"cume_dist()", "pg_catalog.cume_dist()",
		"rank()", "pg_catalog.rank()",
	).Replace(projection)
	queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+qualified+" FROM t ORDER BY id", queryWindowRankRows()))
}

func queryWindowRankSingleton(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, suffix := range []string{"", " WHERE FALSE", " FROM empty_t"} {
		var rows [][]any
		if suffix == "" {
			rows = [][]any{{int64(1), int64(1), float64(0), float64(1)}}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT rank() OVER(),dense_rank() OVER(),percent_rank() OVER(),cume_dist() OVER()"+suffix,
			[]string{"rank", "dense_rank", "percent_rank", "cume_dist"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, rows))
	}
	queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k)")+" FROM t WHERE id=3", [][]any{{int64(3), int64(1), int64(1), float64(0), float64(1)}}))
}

func TestReferenceQueryWindowRankExecution(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		t.Run("basic", func(t *testing.T) { queryWindowRankBasic(t, p, b) })
		t.Run("singleton-empty", func(t *testing.T) { queryWindowRankSingleton(t, p, b) })
		for _, over := range []string{"()", "(ORDER BY 1)"} {
			t.Run("all-peers-"+over, func(t *testing.T) {
				rows := make([][]any, 7)
				for i := range rows {
					rows[i] = []any{int64(i + 1), int64(1), int64(1), float64(0), float64(1)}
				}
				queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+queryWindowRankProjection(over)+" FROM t ORDER BY id", rows))
			})
		}
		steps := []fixture.Step{
			queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(ORDER BY CASE WHEN id=1 THEN 10 ELSE 20 END)")+" FROM t WHERE id<=3 ORDER BY id", [][]any{{int64(1), int64(1), int64(1), float64(0), float64(1) / 3}, {int64(2), int64(2), int64(2), float64(.5), float64(1)}, {int64(3), int64(2), int64(2), float64(.5), float64(1)}}),
			queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k,id)")+" FROM t WHERE p='a' ORDER BY id", [][]any{{int64(1), int64(1), int64(1), float64(0), float64(1) / 3}, {int64(2), int64(2), int64(2), float64(.5), float64(2) / 3}, {int64(3), int64(3), int64(3), float64(1), float64(1)}}),
			queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k)")+" FROM t WHERE id<>2 AND p='a' ORDER BY id", [][]any{{int64(1), int64(1), int64(1), float64(0), float64(.5)}, {int64(3), int64(2), int64(2), float64(1), float64(1)}}),
			queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k)")+" FROM t ORDER BY id LIMIT 2", queryWindowRankRows()[:2]),
			fixture.Select("SELECT id,rank() OVER(PARTITION BY p ORDER BY k) AS a,rank() OVER(PARTITION BY p ORDER BY k DESC) AS b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(1), int64(2)}, {int64(3), int64(3), int64(1)}, {int64(4), int64(1), int64(2)}, {int64(5), int64(2), int64(1)}, {int64(6), int64(1), int64(1)}, {int64(7), int64(1), int64(1)}}),
			fixture.Select("SELECT p,count(*),rank() OVER(ORDER BY count(*) DESC),percent_rank() OVER(ORDER BY count(*) DESC),cume_dist() OVER(ORDER BY count(*) DESC) FROM t GROUP BY p ORDER BY p NULLS LAST", []string{"p", "count", "rank", "percent_rank", "cume_dist"}, []string{"TEXT", "INT8", "INT8", "FLOAT8", "FLOAT8"}, [][]any{{"a", int64(3), int64(1), float64(0), float64(1) / 3}, {"b", int64(2), int64(2), float64(.5), float64(1)}, {nil, int64(2), int64(2), float64(.5), float64(1)}}),
			fixture.Select("SELECT id FROM t ORDER BY rank() OVER(ORDER BY id DESC)", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}}),
			fixture.Select("SELECT rank(15) WITHIN GROUP(ORDER BY k),dense_rank(15) WITHIN GROUP(ORDER BY k),percent_rank(15) WITHIN GROUP(ORDER BY k),cume_dist(15) WITHIN GROUP(ORDER BY k) FROM t WHERE p='a'", []string{"rank", "dense_rank", "percent_rank", "cume_dist"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(3), int64(2), float64(2) / 3, float64(3) / 4}}),
		}
		for i, s := range steps {
			t.Run(fmt.Sprintf("consumer-%d", i), func(t *testing.T) { queryWindowRoutes(t, p, b, s) })
		}
		for _, typ := range []string{"NUMERIC", "CHAR(2)"} {
			t.Run("typed-peers-"+typ, func(t *testing.T) {
				queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE typed_rank(id INT,v "+typ+")", 0))
				defer queryWindowRun(t, p, b, fixture.Exec("DROP TABLE typed_rank", 0))
				values := "(1,1.0),(2,1.00),(3,2)"
				if typ == "CHAR(2)" {
					values = "(1,'x'),(2,'x '),(3,'y')"
				}
				queryWindowRun(t, p, b, fixture.Exec("INSERT INTO typed_rank VALUES"+values, 3))
				queryWindowRoutes(t, p, b, fixture.Select("SELECT id,rank() OVER(ORDER BY v) AS r,dense_rank() OVER(ORDER BY v) AS d FROM typed_rank ORDER BY id", []string{"id", "r", "d"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1)}, {int64(3), int64(3), int64(2)}}))
			})
		}
	})
}
