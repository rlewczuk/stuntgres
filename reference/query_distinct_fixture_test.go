package reference_test

import (
	"database/sql"
	"testing"
)

// queryDistinctScenario keeps a single SQL/assertion body for both backends.
func queryDistinctScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func queryDistinctSetup(t *testing.T, run func(referenceStep)) {
	t.Helper()
	run(referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,1,10),(2,1,10),(3,1,20),(4,NULL,10),(5,NULL,10),(6,NULL,NULL),(7,NULL,NULL)", 7))
}

func queryDistinctBoth(run func(referenceStep), s referenceStep) {
	run(s)
	run(referencePrepared(s))
}

func queryDistinctOrdered() referenceStep {
	return referenceSelect("SELECT DISTINCT a,b FROM t ORDER BY a ASC NULLS LAST,b DESC NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}, {int64(1), int64(10)}, {nil, nil}, {nil, int64(10)}})
}

func queryDistinctAnalysis(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, want referenceDiagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError(q, want)
		s.selectRows, s.errorStage = true, "execute"
		if prepared {
			s = referencePrepared(s)
			s.errorStage = "prepare"
		}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}
