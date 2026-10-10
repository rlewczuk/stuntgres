package reference_test

import "testing"

func TestReferenceCountExpressionCapabilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
	for _, prepared := range []bool{false, true} {
		step := referenceSelect("SELECT count(*)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
		step.prepared = prepared
		p.step(t, step)
	}
	for _, tc := range []struct {
		q, name string
		rows    [][]any
	}{
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
				if tc.q == "SELECT count(*) FILTER (WHERE v>1) FROM t" || tc.q == "SELECT count(DISTINCT v) FROM t" || tc.q == "SELECT count(ALL v) FROM t" || tc.q == "SELECT count(v ORDER BY v) FROM t" {
					native = referenceSelect(tc.q, []string{tc.name}, []string{"INT8"}, tc.rows)
				}
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
	t.Run("SELECT sum(v) FROM t", func(t *testing.T) {
		for _, backend := range p.backends() {
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT sum(v) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		}
	})
}

func TestReferenceQueryAggregateExclusionPrefixEffects(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	// The LOCAL-unit aggregate is legal; a malformed suffix must still prevent
	// the preceding INSERT from publishing, including through Prepare.
	for _, prepared := range []bool{false, true} {
		failure := referenceError("INSERT INTO t VALUES(1);SELECT count(*) WHERE", "42601")
		failure.prepared = prepared
		p.step(t, failure)
		p.step(t, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
	}
	// SUM is now admitted. A malformed SUM suffix still rejects the whole input
	// before its preceding INSERT can publish, through direct and prepared routes.
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO t VALUES(1);SELECT sum(v) FROM t WHERE"
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: referencePosition(len(q) + 1)})
			failure.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, failure)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		}
	}
	for _, q := range []string{
		"SELECT count(*) OVER () FROM t",
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
