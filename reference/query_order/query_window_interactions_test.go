package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowInteractions(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,row_number() OVER(PARTITION BY p ORDER BY k,id) AS rn,count(*) OVER(PARTITION BY p ORDER BY k) AS n FROM t WHERE id<>2 ORDER BY id", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(3), int64(2), int64(2)}, {int64(4), int64(1), int64(1)}, {int64(5), int64(2), int64(2)}, {int64(6), int64(1), int64(2)}, {int64(7), int64(2), int64(2)}}),
			fixture.Select("SELECT p,count(*),row_number() OVER(ORDER BY count(*) DESC,p NULLS LAST),count(*) OVER() FROM t GROUP BY p HAVING count(*)>1 ORDER BY p NULLS LAST", []string{"p", "count", "row_number", "count"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"a", int64(3), int64(1), int64(3)}, {"b", int64(2), int64(2), int64(3)}, {nil, int64(2), int64(3), int64(3)}}),
			fixture.Select("SELECT p,count(*),row_number() OVER(ORDER BY count(*) DESC,p NULLS LAST),count(*) OVER() FROM t GROUP BY p HAVING count(*)>2 ORDER BY p NULLS LAST", []string{"p", "count", "row_number", "count"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"a", int64(3), int64(1), int64(1)}}),
			fixture.Select("SELECT DISTINCT p,count(*) OVER(PARTITION BY p) AS n FROM t ORDER BY p NULLS LAST", []string{"p", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(3)}, {"b", int64(2)}, {nil, int64(2)}}),
			fixture.Select("SELECT DISTINCT p,row_number() OVER(PARTITION BY p ORDER BY id) AS rn FROM t ORDER BY p NULLS LAST,rn", []string{"p", "rn"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(2)}, {"a", int64(3)}, {"b", int64(1)}, {"b", int64(2)}, {nil, int64(1)}, {nil, int64(2)}}),
			fixture.Select("SELECT id,count(*) OVER() FROM t ORDER BY id LIMIT 2 OFFSET 1", []string{"id", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(7)}, {int64(3), int64(7)}}),
			fixture.Select("SELECT id FROM t ORDER BY row_number() OVER(ORDER BY id DESC)", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}}),
			fixture.Select("SELECT id,row_number() OVER(ORDER BY id)+1 AS rn FROM t ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, queryWindowValues(2, 3, 4, 5, 6, 7, 8)),
			fixture.Select("(SELECT id,row_number() OVER(ORDER BY id) AS rn FROM t WHERE id<=2) UNION ALL (SELECT id,row_number() OVER(ORDER BY id) FROM t WHERE id=3) ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(1)}}),
			fixture.Select("SELECT id,rn FROM (SELECT id,row_number() OVER(ORDER BY id) AS rn FROM t) s WHERE rn<=2 ORDER BY id", []string{"id", "rn"}, []string{"INT4", "INT8"}, queryWindowValues(1, 2)),
			fixture.Select("SELECT EXISTS(SELECT row_number() OVER() FROM t)", []string{"exists"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			queryWindowRoutes(t, p, b, s)
		}
		for _, tc := range []struct {
			op   string
			rows [][]any
		}{
			{"UNION", [][]any{{int64(1)}, {int64(2)}}},
			{"UNION ALL", [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}}},
			{"INTERSECT ALL", [][]any{{int64(1)}, {int64(2)}}},
			{"EXCEPT ALL", nil},
		} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER(ORDER BY id) AS rn FROM t WHERE id<=2 "+tc.op+" SELECT row_number() OVER(ORDER BY id) FROM t WHERE id>=6 ORDER BY rn", []string{"rn"}, []string{"INT8"}, tc.rows))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE dst(id INT,rn BIGINT,n BIGINT)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO dst SELECT id,row_number() OVER(PARTITION BY p ORDER BY k,id),count(*) OVER(PARTITION BY p ORDER BY k) FROM t", 7)
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
			queryWindowRun(t, p, b, queryWindowResult("SELECT id,rn,n FROM dst ORDER BY id"))
			queryWindowRun(t, p, b, fixture.Exec("DELETE FROM dst", 7))
			s = fixture.Exec("INSERT INTO dst SELECT id,row_number() OVER(),count(*) OVER() FROM empty_t", 0)
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE small_dst(rn SMALLINT)", 0))
		queryWindowRoutes(t, p, b, fixture.Exec("INSERT INTO small_dst SELECT row_number() OVER()", 1))
		queryWindowRun(t, p, b, fixture.Select("SELECT rn FROM small_dst", []string{"rn"}, []string{"INT2"}, [][]any{{int64(1)}, {int64(1)}}))
	})
}

func TestReferenceQueryWindowTypedKeys(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE keys(id INT,a INT,b INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO keys VALUES(1,1,1),(2,1,1),(3,1,2),(4,NULL,1),(5,NULL,1)", 5))
		for _, tc := range []struct {
			order string
			v     []int64
		}{
			{"a,b", []int64{2, 2, 3, 5, 5}}, {"a DESC NULLS LAST,b DESC", []int64{3, 3, 1, 5, 5}}, {"a NULLS FIRST,b", []int64{4, 4, 5, 2, 2}},
		} {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER(ORDER BY "+tc.order+") AS n FROM keys ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(tc.v...)))
		}
		for _, tc := range []struct {
			typ, values string
			v           []int64
		}{
			{"NUMERIC", "1.0,1.00,2.0", []int64{2, 2, 1}},
			{"CHAR(2)", "'x','x ','y'", []int64{2, 2, 1}},
			{"TEXT", "'x','x ','y'", []int64{1, 1, 1}},
			{"DOUBLE PRECISION", "'-0','0','NaN'", []int64{2, 2, 1}},
			{"JSONB", "'{\"a\":1}','{\"a\":1}','{\"a\":2}'", []int64{2, 2, 1}},
			{"INT[]", "'{1}','{1}','{2}'", []int64{2, 2, 1}},
		} {
			queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE typed(id INT,k "+tc.typ+")", 0))
			parts := strings.Split(tc.values, ",")
			queryWindowRun(t, p, b, fixture.Exec("INSERT INTO typed VALUES(1,"+parts[0]+"),(2,"+parts[1]+"),(3,"+parts[2]+")", 3))
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,count(*) OVER(PARTITION BY k) AS n FROM typed ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(tc.v...)))
			queryWindowRun(t, p, b, fixture.Exec("DROP TABLE typed", 0))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE TYPE window_e AS ENUM('z','a')", 0))
		queryWindowRun(t, p, b, fixture.Exec("CREATE DOMAIN window_d AS NUMERIC", 0))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ordered(id INT,e window_e,d window_d)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ordered VALUES(1,'a',1.0),(2,'z',1.00),(3,'z',2)", 3))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,row_number() OVER(ORDER BY e,id) AS rn,count(*) OVER(PARTITION BY d) AS n FROM ordered ORDER BY id", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(3), int64(2)}, {int64(2), int64(1), int64(2)}, {int64(3), int64(2), int64(1)}}))
	})
}
