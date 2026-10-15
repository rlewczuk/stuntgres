package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowOffsetScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func queryWindowOffsetSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wo(id INT NOT NULL,p TEXT,v INT,n INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wo VALUES(1,'a',10,1),(2,'a',NULL,0),(3,'a',30,-1),(4,'a',40,NULL),(5,'b',50,2),(6,'b',NULL,-2)", 6))
}

func queryWindowOffsetVector(t *testing.T, p *fixture.Pair, b fixture.Backend, expr string, values []any, tail string) {
	t.Helper()
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{int64(i + 1), v}
	}
	queryWindowRoutes(t, p, b, fixture.Select("SELECT id,"+expr+" AS x FROM wo "+tail+" ORDER BY id", []string{"id", "x"}, []string{"INT4", "INT4"}, rows))
}

func queryWindowOffsetCore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct {
		expr   string
		values []any
	}{
		{"lead(v) OVER w", []any{nil, int64(30), int64(40), nil, nil, nil}},
		{"lag(v) OVER w", []any{nil, int64(10), nil, int64(30), nil, int64(50)}},
		{"lead(v,n,id*100) OVER w", []any{nil, nil, nil, nil, int64(500), int64(600)}},
		{"lag(v,n,id*100) OVER w", []any{int64(100), nil, int64(40), nil, int64(500), int64(600)}},
		{"lead(v,0,-1) OVER w", []any{int64(10), nil, int64(30), int64(40), int64(50), nil}},
		{"lag(v,0,-1) OVER w", []any{int64(10), nil, int64(30), int64(40), int64(50), nil}},
		{"lead(v,-1,id*100) OVER w", []any{int64(100), int64(10), nil, int64(30), int64(500), int64(50)}},
		{"lag(v,-1,id*100) OVER w", []any{nil, int64(30), int64(40), int64(400), nil, int64(600)}},
		{"lead(id*10,1,-id) OVER w", []any{int64(20), int64(30), int64(40), int64(-4), int64(60), int64(-6)}},
		{"lead(v) OVER(PARTITION BY p ORDER BY id DESC)", []any{nil, int64(10), nil, int64(30), nil, int64(50)}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			queryWindowOffsetVector(t, p, b, tc.expr, tc.values, "WINDOW w AS(PARTITION BY p ORDER BY id)")
		})
	}
	for _, fn := range []string{"lead", "lag"} {
		for _, n := range []string{"'-2147483648'::int", "2147483647"} {
			queryWindowOffsetVector(t, p, b, fn+"(v,"+n+",id*100) OVER w", []any{int64(100), int64(200), int64(300), int64(400), int64(500), int64(600)}, "WINDOW w AS(PARTITION BY p ORDER BY id)")
		}
	}
}

func TestReferenceQueryWindowOffsetExecution(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		queryWindowOffsetCore(t, p, b)
		for i, s := range []fixture.Step{
			fixture.Select("SELECT lead(7) OVER(),lag(7) OVER(),lead(7,0) OVER(),lag(7,-1,9) OVER()", []string{"lead", "lag", "lead", "lag"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{nil, nil, int64(7), int64(9)}}),
			fixture.Select("SELECT lead(v) OVER(),lag(v,0) OVER() FROM wo WHERE FALSE", []string{"lead", "lag"}, []string{"INT4", "INT4"}, nil),
			fixture.Select("SELECT lead(v) OVER(),lag(v,0) OVER() FROM wo LIMIT 0", []string{"lead", "lag"}, []string{"INT4", "INT4"}, nil),
			fixture.Select("SELECT p,lead(sum(v),1,sum(v)) OVER(ORDER BY p) AS x FROM wo GROUP BY p HAVING count(*)>1 ORDER BY p", []string{"p", "x"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(50)}, {"b", int64(50)}}),
			fixture.Select("SELECT id FROM wo ORDER BY lead(id,0) OVER(ORDER BY id) DESC LIMIT 2", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(5)}}),
			fixture.Select("SELECT DISTINCT lead(v,0) OVER() AS x FROM wo ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(30)}, {int64(40)}, {int64(50)}, {nil}}),
			fixture.Select("SELECT lead(1,0) OVER() AS x UNION ALL SELECT lag(2,0) OVER() ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		} {
			t.Run(fmt.Sprint(i), func(t *testing.T) { queryWindowRoutes(t, p, b, s) })
		}
		s := fixture.Select("SELECT lead(v,0) OVER() AS x FROM wo", []string{"x"}, []string{"INT4"}, [][]any{{int64(10)}, {nil}, {int64(30)}, {int64(40)}, {int64(50)}, {nil}})
		s.Bag = true
		queryWindowRoutes(t, p, b, s)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,lead(id,0) OVER(ORDER BY p) AS x FROM wo ORDER BY id", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}, {int64(5), int64(5)}, {int64(6), int64(6)}}))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wo VALUES(7,NULL,70,1),(8,NULL,80,1)", 2))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,lead(v) OVER(PARTITION BY p ORDER BY id) AS x,lag(v) OVER(PARTITION BY p ORDER BY id DESC) AS y FROM wo WHERE p IS NULL ORDER BY id", []string{"id", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(80), int64(80)}, {int64(8), nil, nil}}))
	})
}
