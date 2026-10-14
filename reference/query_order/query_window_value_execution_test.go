package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func queryWindowValueSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wv(id INT NOT NULL,p TEXT,k INT,v INT,n INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wv VALUES(1,'a',1,10,1),(2,'a',1,10,2),(3,'a',2,NULL,1),(4,'a',3,40,3),(5,'a',3,40,6),(6,'b',1,NULL,1),(7,'b',2,70,NULL)", 7))
}

func queryWindowValueDefault(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(v) OVER w AS f,last_value(v) OVER w AS l,nth_value(v,2) OVER w AS n2,nth_value(v,4) OVER w AS n4,row_number() OVER(ORDER BY id) AS rn,count(*) OVER w AS c FROM wv WINDOW w AS(PARTITION BY p ORDER BY k) ORDER BY id",
		[]string{"id", "f", "l", "n2", "n4", "rn", "c"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT8", "INT8"}, [][]any{
			{int64(1), int64(10), int64(10), int64(10), nil, int64(1), int64(2)},
			{int64(2), int64(10), int64(10), int64(10), nil, int64(2), int64(2)},
			{int64(3), int64(10), nil, int64(10), nil, int64(3), int64(3)},
			{int64(4), int64(10), int64(40), int64(10), int64(40), int64(4), int64(5)},
			{int64(5), int64(10), int64(40), int64(10), int64(40), int64(5), int64(5)},
			{int64(6), nil, nil, nil, nil, int64(6), int64(1)},
			{int64(7), nil, int64(70), int64(70), nil, int64(7), int64(2)},
		}))
}

func TestReferenceQueryWindowValueExecution(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowValueSeed(t, p, b)
		t.Run("default", func(t *testing.T) { queryWindowValueDefault(t, p, b) })
		t.Run("exists-demands-window", func(t *testing.T) {
			// PG18 simplify_EXISTS_query refuses queries with window functions:
			// the window target is demanded even though EXISTS only needs a row.
			queryWindowValueDiagnosticRoutes(t, p, b, "SELECT EXISTS(SELECT nth_value(v,0) OVER() FROM wv) AS e", fixture.Diagnostic{Code: "22016", Message: "argument of nth_value must be greater than zero"}, "execute")
		})
		steps := []fixture.Step{
			fixture.Select("SELECT first_value(1) OVER(),last_value(1) OVER(),nth_value(1,1) OVER()", []string{"first_value", "last_value", "nth_value"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}),
			fixture.Select("SELECT first_value(NULL::text) OVER(),last_value(NULL::text) OVER(),nth_value(NULL::text,1) OVER()", []string{"first_value", "last_value", "nth_value"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}),
			fixture.Select("SELECT first_value(v) OVER(),last_value(v) OVER(),nth_value(v,0) OVER() FROM wv WHERE FALSE", []string{"first_value", "last_value", "nth_value"}, []string{"INT4", "INT4", "INT4"}, nil),
			fixture.Select("SELECT nth_value(v,0) OVER() FROM wv LIMIT 0", []string{"nth_value"}, []string{"INT4"}, nil),
			fixture.Select("SELECT id,first_value(v) OVER w AS a,first_value(v+1) OVER w AS b,nth_value(v,1) OVER w AS c,nth_value(v,3) OVER w AS d FROM wv WINDOW w AS(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id", []string{"id", "a", "b", "c", "d"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(11), int64(10), nil}, {int64(2), int64(10), int64(11), int64(10), nil}, {int64(3), int64(10), int64(11), int64(10), nil}, {int64(4), int64(10), int64(11), int64(10), nil}, {int64(5), int64(10), int64(11), int64(10), nil}, {int64(6), nil, nil, nil, nil}, {int64(7), nil, nil, nil, nil}}),
			fixture.Select("SELECT DISTINCT nth_value(v,2) OVER(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS x FROM wv ORDER BY nth_value(v,2) OVER(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) LIMIT 1", []string{"x"}, []string{"INT4"}, [][]any{{int64(10)}}),
			fixture.Select("SELECT id FROM wv WHERE id<=3 ORDER BY last_value(id) OVER(ORDER BY id) DESC LIMIT 2", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}}),
			fixture.Select("SELECT p,first_value(sum(v)) OVER(ORDER BY p) AS f,nth_value(sum(v),count(*)::int) OVER(ORDER BY p) AS n FROM wv GROUP BY p HAVING count(*)>=2 ORDER BY p", []string{"p", "f", "n"}, []string{"TEXT", "INT8", "INT8"}, [][]any{{"a", int64(100), nil}, {"b", int64(100), int64(70)}}),
			fixture.Select("SELECT id,first_value((SELECT x.v FROM wv x WHERE x.id=w.id)) OVER(ORDER BY id) AS f,nth_value(v,(SELECT x.n FROM wv x WHERE x.id=w.id)) OVER(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS n FROM wv w ORDER BY id", []string{"id", "f", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(10)}, {int64(3), int64(10), int64(10)}, {int64(4), int64(10), nil}, {int64(5), int64(10), nil}, {int64(6), int64(10), nil}, {int64(7), int64(10), nil}}),
			fixture.Select("SELECT DISTINCT nth_value(v,(SELECT 2)) OVER(ORDER BY id) AS x FROM wv ORDER BY nth_value(v,(SELECT 2)) OVER(ORDER BY id)", []string{"x"}, []string{"INT4"}, [][]any{{int64(10)}, {nil}}),
			fixture.Select("SELECT id,(SELECT nth_value(x.v,w.n) OVER(ORDER BY x.id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM wv x WHERE x.p=w.p ORDER BY x.id LIMIT 1) AS n FROM wv w ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(10)}, {int64(3), int64(10)}, {int64(4), nil}, {int64(5), nil}, {int64(6), nil}, {int64(7), nil}}),
			fixture.Select("SELECT DISTINCT nth_value(v,(SELECT 1)) OVER w AS a,nth_value(v,(SELECT 2)) OVER w AS b FROM wv WINDOW w AS(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY nth_value(v,(SELECT 2)) OVER w", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(10)}, {nil, int64(70)}}),
		}
		for i, s := range steps {
			t.Run(fmt.Sprintf("consumer-%d", i), func(t *testing.T) { queryWindowRoutes(t, p, b, s) })
		}
		t.Run("selected-member", func(t *testing.T) {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(1/(id-3)) OVER w AS f,last_value(1/(id-3)) OVER w AS l,nth_value(1/(id-3),100) OVER w AS absent,nth_value(1/(id-3),NULL::int) OVER w AS null_n FROM wv WHERE id<=4 WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id", []string{"id", "f", "l", "absent", "null_n"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(1), nil, nil}, {int64(2), int64(0), int64(1), nil, nil}, {int64(3), int64(0), int64(1), nil, nil}, {int64(4), int64(0), int64(1), nil, nil}}))
		})
		t.Run("volatile", func(t *testing.T) { queryWindowValueVolatile(t, p, b) })
	})
}
