package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowExcludeSum(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE sums(id INT,v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO sums VALUES(1,NULL),(2,10),(3,-10)", 3))
		for _, tc := range []struct {
			spec string
			rows [][]any
		}{
			{"ROWS CURRENT ROW EXCLUDE NO OTHERS", [][]any{{int64(1), int64(1), nil}, {int64(2), int64(1), int64(10)}, {int64(3), int64(1), int64(-10)}}},
			{"ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS", [][]any{{int64(1), int64(3), int64(0)}, {int64(2), int64(3), int64(0)}, {int64(3), int64(3), int64(0)}}},
			{"ROWS CURRENT ROW EXCLUDE GROUP", [][]any{{int64(1), int64(0), nil}, {int64(2), int64(0), nil}, {int64(3), int64(0), nil}}},
		} {
			q := "SELECT id,count(*) OVER(" + tc.spec + ") AS n,sum(v) OVER(" + tc.spec + ") AS s FROM sums ORDER BY id"
			queryWindowRoutes(t, p, b, fixture.Select(q, []string{"id", "n", "s"}, []string{"INT4", "INT8", "INT8"}, tc.rows))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE bad_values(id INT,v TEXT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO bad_values VALUES(1,'oops'),(2,'bad')", 2))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,sum(v::INT) OVER(ROWS CURRENT ROW EXCLUDE GROUP) AS s FROM bad_values ORDER BY id", []string{"id", "s"}, []string{"INT4", "INT8"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
	})
}

func TestReferenceQueryWindowExcludeMetadata(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE typed(s SMALLINT NOT NULL,i INT NOT NULL,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO typed VALUES(2,2,2,2,2,2)", 1))
		for _, empty := range []bool{false, true} {
			q := "SELECT count(*) OVER w AS c,row_number() OVER w AS rn,sum(s) OVER w AS ss,sum(i) OVER w AS si,sum(b) OVER w AS sb,sum(n) OVER w AS sn,sum(r) OVER w AS sr,sum(d) OVER w AS sd FROM typed"
			if empty {
				q += " WHERE FALSE"
			}
			q += " WINDOW w AS(ROWS CURRENT ROW EXCLUDE CURRENT ROW)"
			rows := [][]any{{int64(0), int64(1), nil, nil, nil, nil, nil, nil}}
			if empty {
				rows = nil
			}
			s := fixture.Select(q, []string{"c", "rn", "ss", "si", "sb", "sn", "sr", "sd"}, []string{"INT8", "INT8", "INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, rows)
			s.CheckMetadata = queryWindowExcludeMetadata(b.PG, false, false, true, true, true, true, true, true)
			queryWindowRoutes(t, p, b, s)
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT sum(s) OVER w AS ss,sum(i) OVER w AS si,sum(b) OVER w AS sb,sum(n) OVER w AS sn,sum(r) OVER w AS sr,sum(d) OVER w AS sd FROM typed WINDOW w AS(ROWS CURRENT ROW EXCLUDE TIES)", []string{"ss", "si", "sb", "sn", "sr", "sd"}, []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(2), int64(2), "2", "2", float64(2), float64(2)}}))
	})
}

func TestReferenceQueryWindowExcludePipeline(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		q := "SELECT id,count(*) OVER w AS n,sum(v) OVER w AS s,sum(v+1) OVER w AS plus,sum(v::BIGINT) OVER w AS big,row_number() OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE GROUP) AS rn,count(*) OVER(ORDER BY k ROWS CURRENT ROW EXCLUDE GROUP) AS empty FROM ex_t WINDOW w AS(ORDER BY k RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) ORDER BY id"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"id", "n", "s", "plus", "big", "rn", "empty"}, []string{"INT4", "INT8", "INT8", "INT8", "NUMERIC", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(40), int64(42), "40", int64(1), int64(0)}, {int64(2), int64(2), int64(50), int64(52), "50", int64(2), int64(0)}, {int64(3), int64(3), int64(60), int64(63), "60", int64(3), int64(0)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT k,sum(count(*)) OVER(ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS) AS n FROM ex_t GROUP BY k HAVING count(*)>0 ORDER BY k", []string{"k", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "3"}, {int64(2), "3"}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT DISTINCT sum(v) OVER(ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS) AS s FROM ex_t WHERE id>1 ORDER BY s DESC LIMIT 1", []string{"s"}, []string{"INT8"}, [][]any{{int64(50)}}))
		// Independent result bag: exclusions never remove output occurrences or
		// influence row_number, even when all aggregate frames are empty.
		queryWindowRoutes(t, p, b, fixture.Select("SELECT rn FROM (SELECT row_number() OVER(ORDER BY k ROWS CURRENT ROW EXCLUDE GROUP) AS rn FROM ex_t) x ORDER BY rn", []string{"rn"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}

func TestReferenceQueryWindowExcludeVolatileArguments(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		for _, prepared := range []bool{false, true} {
			queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_volatile START WITH 1", 0))
			q := "SELECT sum(a+b) AS total FROM (SELECT sum(nextval('ex_volatile')) OVER w AS a,sum(nextval('ex_volatile')) OVER w AS b FROM ex_t WINDOW w AS(ORDER BY id ROWS CURRENT ROW EXCLUDE TIES)) x"
			s := fixture.Select(q, []string{"total"}, []string{"NUMERIC"}, [][]any{{"21"}})
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, fixture.Select("SELECT last_value FROM ex_volatile", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(6)}}))
			queryWindowRun(t, p, b, fixture.Exec("DROP SEQUENCE ex_volatile", 0))
		}
	})
}
