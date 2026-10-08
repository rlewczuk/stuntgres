package reference_test

import "testing"

func TestReferenceCountExpressionCapabilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
	for _, tc := range []struct {
		q, name string
		rows    [][]any
	}{
		{"SELECT count(*)", "count", [][]any{{int64(1)}}},
		{"SELECT sum(v) FROM t", "sum", [][]any{{int64(4)}}},
		{"SELECT count(*) FILTER (WHERE v>1) FROM t", "count", [][]any{{int64(1)}}},
		{"SELECT count(DISTINCT v) FROM t", "count", [][]any{{int64(2)}}},
		{"SELECT count(ALL v) FROM t", "count", [][]any{{int64(3)}}},
		{"SELECT count(v ORDER BY v) FROM t", "count", [][]any{{int64(3)}}},
		{"SELECT count(*) OVER () FROM t", "count", [][]any{{int64(3)}, {int64(3)}, {int64(3)}}},
		{"SELECT count(*) FROM t GROUP BY ROLLUP(v)", "count", [][]any{{int64(2)}, {int64(1)}, {int64(3)}}},
		{"SELECT count(*) FROM t GROUP BY CUBE(v)", "count", [][]any{{int64(2)}, {int64(1)}, {int64(3)}}},
		{"SELECT count(*) FROM t GROUP BY GROUPING SETS ((v))", "count", [][]any{{int64(2)}, {int64(1)}}},
		{"SELECT count(*) FROM t GROUP BY ()", "count", [][]any{{int64(3)}}},
		{"SELECT count(*) FROM t GROUP BY DISTINCT v", "count", [][]any{{int64(2)}, {int64(1)}}},
		{"SELECT count(*) FROM t GROUP BY ALL v", "count", [][]any{{int64(2)}, {int64(1)}}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				native := referenceError(tc.q, "0A000")
				native.selectRows = true
				native.prepared = prepared
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
				oracle := referenceSelect(tc.q, []string{tc.name}, []string{"INT8"}, tc.rows)
				oracle.bag = true
				oracle.prepared = prepared
				// Intentional admission difference, not parity or fake native aggregates.
				if p.postgres != nil {
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, oracle)
				}
			}
		})
	}
}

func TestReferenceQueryAggregateExclusionPrefixEffects(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	for _, q := range []string{
		"SELECT count(*)", "SELECT sum(v) FROM t",
		"SELECT count(*) FILTER (WHERE TRUE) FROM t", "SELECT count(DISTINCT v) FROM t",
		"SELECT count(* ORDER BY v) FROM t", "SELECT count(*) OVER () FROM t", "SELECT count(ALL v) FROM t",
		"SELECT v FROM t GROUP BY ROLLUP(v)", "SELECT v FROM t GROUP BY CUBE(v)",
		"SELECT v FROM t GROUP BY GROUPING SETS ((v))", "SELECT v FROM t GROUP BY ()",
		"SELECT v FROM t GROUP BY DISTINCT v", "SELECT v FROM t GROUP BY ALL v",
	} {
		t.Run(q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				failure := referenceError("INSERT INTO t VALUES(1);"+q, "0A000")
				if prepared {
					queryAggregatePrepareRejection(t, p, failure.query, failure.state)
				} else {
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
				}
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
			}
		})
	}
}

func queryAggregatePrepareRejection(t *testing.T, p *referencePair, query, state string) {
	t.Helper()
	stmt, err := p.memory.PrepareContext(p.ctx, query)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed analysis exposed a statement", query)
	}
	if err == nil {
		t.Fatal("analysis unexpectedly prepared", query)
	}
	referenceState(t, err, state)
}
