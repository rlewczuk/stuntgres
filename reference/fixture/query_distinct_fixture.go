package fixture

import (
	"database/sql"
	"testing"
)

// QueryDistinctScenario keeps a single SQL/assertion body for both backends.
func QueryDistinctScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func QueryDistinctSetup(t *testing.T, run func(Step)) {
	t.Helper()
	run(Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	run(Exec("INSERT INTO t VALUES(1,1,10),(2,1,10),(3,1,20),(4,NULL,10),(5,NULL,10),(6,NULL,NULL),(7,NULL,NULL)", 7))
}

func QueryDistinctBoth(run func(Step), s Step) {
	run(s)
	run(Prepared(s))
}

func QueryDistinctOrdered() Step {
	return Select("SELECT DISTINCT a,b FROM t ORDER BY a ASC NULLS LAST,b DESC NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}, {int64(1), int64(10)}, {nil, nil}, {nil, int64(10)}})
}

func QueryDistinctAnalysis(t *testing.T, p *Pair, b Backend, c *sql.Conn, q string, want Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := ImplicitError(q, want)
		s.SelectRows, s.ErrorStage = true, "execute"
		if prepared {
			s = Prepared(s)
			s.ErrorStage = "prepare"
		}
		RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
