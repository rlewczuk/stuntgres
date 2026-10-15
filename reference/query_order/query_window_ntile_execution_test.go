package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowNtileSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE nt(id INT NOT NULL,p TEXT,b INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO nt VALUES(1,'a',NULL),(2,'a',2),(3,'a',0),(4,'a',NULL),(5,'a',-1),(6,'b',2),(7,'b',0)", 7))
}

func queryWindowNtileLabels(q string, labels ...any) fixture.Step {
	rows := make([][]any, len(labels))
	for i, label := range labels {
		rows[i] = []any{int64(i + 1), label}
	}
	return fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT4"}, rows)
}

func TestReferenceQueryWindowNtileExecution(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		for _, tc := range []struct {
			name, buckets, where, order string
			labels                      []int64
		}{
			{"divisible", "3", "id<=6", "id", []int64{1, 1, 2, 2, 3, 3}},
			{"nondivisible", "3", "TRUE", "id", []int64{1, 1, 1, 2, 2, 3, 3}},
			{"larger-first", "4", "id<=6", "id", []int64{1, 1, 2, 2, 3, 4}},
			{"more-buckets", "5", "id<=3", "id", []int64{1, 2, 3}},
			{"max-int", "2147483647", "id<=3", "id", []int64{1, 2, 3}},
			{"descending", "4", "id<=6", "id DESC", []int64{4, 3, 2, 2, 1, 1}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT id,ntile(" + tc.buckets + ") OVER(ORDER BY " + tc.order + ") AS n FROM nt WHERE " + tc.where + " ORDER BY id"
				queryWindowRoutes(t, p, b, fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT4"}, queryWindowValues(tc.labels...)))
			})
		}
		for _, expr := range []string{"1", "5", "'2'", "2::smallint"} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile("+expr+") OVER()", []string{"ntile"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE DOMAIN nt_int AS INTEGER", 0))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile('2'::nt_int) OVER()", []string{"ntile"}, []string{"INT4"}, [][]any{{int64(1)}}))
		queryWindowRoutes(t, p, b, queryWindowNtileLabels("SELECT id,ntile(NULL) OVER(ORDER BY id) AS n FROM nt WHERE id<=3 ORDER BY id", nil, nil, nil))
		for _, buckets := range []string{"0", "-1", "NULL"} {
			for _, suffix := range []string{" WHERE FALSE", " LIMIT 0"} {
				queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile("+buckets+") OVER() FROM nt"+suffix, []string{"ntile"}, []string{"INT4"}, nil))
			}
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE nt_empty(id INT)", 0))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile(0) OVER() FROM nt_empty", []string{"ntile"}, []string{"INT4"}, nil))
		queryWindowValueDiagnosticRoutes(t, p, b, "SELECT EXISTS(SELECT ntile(0) OVER() FROM nt)", fixture.Diagnostic{Code: "22014", Message: "argument of ntile must be greater than zero"}, "execute")
		queryWindowRoutes(t, p, b, fixture.Select("SELECT EXISTS(SELECT ntile(0) OVER() FROM nt_empty) AS e", []string{"e"}, []string{"BOOL"}, [][]any{{false}}))
		queryWindowSeed(t, p, b)
		for _, over := range []string{"(PARTITION BY p ORDER BY id)", "w", "(base ORDER BY id)"} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,ntile(2) OVER "+over+" AS n FROM t WINDOW base AS(PARTITION BY p),w AS(base ORDER BY id) ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, queryWindowValues(1, 1, 2, 1, 2, 1, 2)))
		}
		s := fixture.Select("SELECT ntile(2) OVER(ORDER BY p) AS n FROM nt WHERE id<=4", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
		s.Bag = true
		queryWindowRoutes(t, p, b, s)
		for i, s := range []fixture.Step{
			fixture.Select("SELECT p,ntile(count(*)::int) OVER(ORDER BY p) AS n FROM nt GROUP BY p HAVING count(*)>=2 ORDER BY p", []string{"p", "n"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}),
			queryWindowNtileLabels("SELECT id,ntile((SELECT 2)) OVER(ORDER BY id) AS n FROM nt WHERE id<=5 ORDER BY id", int64(1), int64(1), int64(1), int64(2), int64(2)),
			fixture.Select("SELECT DISTINCT ntile(2) OVER(ORDER BY id) AS n FROM nt ORDER BY n LIMIT 1", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}),
		} {
			t.Run(fmt.Sprintf("consumer-%d", i), func(t *testing.T) { queryWindowRoutes(t, p, b, s) })
		}
	})
}
