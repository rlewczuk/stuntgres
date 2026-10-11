package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		body(p, backend)
	}
}

func queryWindowRun(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func queryWindowRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		if s.State != "" {
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
		}
		queryWindowRun(t, p, b, s)
	}
}

func queryWindowSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT NOT NULL,p TEXT,k INT)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,'a',10),(2,'a',10),(3,'a',20),(4,'b',5),(5,'b',NULL),(6,NULL,1),(7,NULL,1)", 7),
		fixture.Exec("CREATE TABLE empty_t(id INT NOT NULL,p TEXT,k INT)", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
}

func queryWindowValues(values ...int64) [][]any {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{int64(i + 1), v}
	}
	return rows
}

func queryWindowResult(q string) fixture.Step {
	return fixture.Select(q, []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{
		{int64(1), int64(1), int64(2)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(3), int64(3)},
		{int64(4), int64(1), int64(1)}, {int64(5), int64(2), int64(2)}, {int64(6), int64(1), int64(2)}, {int64(7), int64(2), int64(2)},
	})
}

func TestReferenceQueryWindowExecution(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		for _, tc := range []struct {
			expr, name string
			values     []int64
		}{
			{"row_number() OVER(PARTITION BY p ORDER BY k,id)", "rn", []int64{1, 2, 3, 1, 2, 1, 2}},
			{"count(*) OVER(PARTITION BY p ORDER BY k)", "n", []int64{2, 2, 3, 1, 2, 2, 2}},
			{"count(*) OVER(PARTITION BY p)", "n", []int64{3, 3, 3, 2, 2, 2, 2}},
			{"count(*) OVER()", "n", []int64{7, 7, 7, 7, 7, 7, 7}},
			{"count(*) OVER(ORDER BY 1)", "n", []int64{7, 7, 7, 7, 7, 7, 7}},
			{"count(*) OVER(PARTITION BY p ORDER BY k,id)", "n", []int64{1, 2, 3, 1, 2, 1, 2}},
		} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,"+tc.expr+" AS "+tc.name+" FROM t ORDER BY id", []string{"id", tc.name}, []string{"INT4", "INT8"}, queryWindowValues(tc.values...)))
		}
		queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,row_number() OVER(PARTITION BY p ORDER BY k,id) AS rn,count(*) OVER(PARTITION BY p ORDER BY k) AS n FROM t ORDER BY id"))
		for _, q := range []string{"SELECT row_number() OVER(),count(*) OVER()", "SELECT row_number() OVER(),count(*) OVER() WHERE FALSE", "SELECT row_number() OVER(),count(*) OVER() FROM empty_t"} {
			var want [][]any
			if q == "SELECT row_number() OVER(),count(*) OVER()" {
				want = [][]any{{int64(1), int64(1)}}
			}
			queryWindowRoutes(t, p, b, fixture.Select(q, []string{"row_number", "count"}, []string{"INT8", "INT8"}, want))
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT count(*) FROM empty_t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		q := "SELECT row_number() OVER(PARTITION BY p ORDER BY k) AS a,row_number() OVER(PARTITION BY p ORDER BY k) AS b FROM t"
		s := fixture.Select(q, []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(1), int64(1)}, {int64(2), int64(2)}, {int64(1), int64(1)}, {int64(2), int64(2)}})
		s.Bag = true
		queryWindowRoutes(t, p, b, s)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,row_number() OVER(PARTITION BY p ORDER BY id) AS a,row_number() OVER(PARTITION BY p ORDER BY id DESC) AS b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(3)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(3), int64(1)}, {int64(4), int64(1), int64(2)}, {int64(5), int64(2), int64(1)}, {int64(6), int64(1), int64(2)}, {int64(7), int64(2), int64(1)}}))
	})
}

func TestReferenceQueryWindowNamed(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		for _, over := range []string{"ordered", "(ordered)", "(base ORDER BY k,id)"} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,row_number() OVER "+over+" AS rn FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id) ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, queryWindowValues(1, 2, 3, 1, 2, 1, 2)))
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER base AS n FROM t WINDOW base AS(PARTITION BY p) ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(3, 3, 3, 2, 2, 2, 2)))
		queryWindowRoutes(t, p, b, fixture.Select(`SELECT id,pg_catalog.row_number() OVER "Window" AS "Rank" FROM t WINDOW "Window" AS(PARTITION BY p ORDER BY k,id) ORDER BY id`, []string{"id", "Rank"}, []string{"INT4", "INT8"}, queryWindowValues(1, 2, 3, 1, 2, 1, 2)))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER() AS over,count(*) OVER() AS partition", []string{"over", "partition"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM t WINDOW w AS () ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER w,(SELECT count(*) OVER w WINDOW w AS ()) FROM t WINDOW w AS (ORDER BY id) ORDER BY id", []string{"row_number", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {int64(4), int64(1)}, {int64(5), int64(1)}, {int64(6), int64(1)}, {int64(7), int64(1)}}))
	})
}
