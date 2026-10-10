package reference_test

import (
	"database/sql"
	"testing"
	"unicode/utf8"
)

func TestReferenceCountExpressionCapabilityBoundaries(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
		groupingSetsBoth(run, referenceSelect("SELECT count(*)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s := groupingSetsBag("SELECT count(*) OVER () FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(3)}, {int64(3)}})
		queryWindowRoutes(t, p, backend, s)
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT count(*) FILTER (WHERE v>1) FROM t", [][]any{{int64(1)}}},
			{"SELECT count(DISTINCT v) FROM t", [][]any{{int64(2)}}},
			{"SELECT count(ALL v) FROM t", [][]any{{int64(3)}}},
			{"SELECT count(v ORDER BY v) FROM t", [][]any{{int64(3)}}},
			{"SELECT count(*) FROM t GROUP BY ROLLUP(v)", [][]any{{int64(2)}, {int64(1)}, {int64(3)}}},
			{"SELECT count(*) FROM t GROUP BY CUBE(v)", [][]any{{int64(2)}, {int64(1)}, {int64(3)}}},
			{"SELECT count(*) FROM t GROUP BY GROUPING SETS ((v))", [][]any{{int64(2)}, {int64(1)}}},
			{"SELECT count(*) FROM t GROUP BY ()", [][]any{{int64(3)}}},
			{"SELECT count(*) FROM t GROUP BY DISTINCT v", [][]any{{int64(2)}, {int64(1)}}},
			{"SELECT count(*) FROM t GROUP BY ALL v", [][]any{{int64(2)}, {int64(1)}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
				groupingSetsBoth(run, groupingSetsBag(tc.q, []string{"count"}, []string{"INT8"}, tc.rows))
			})
		}
		groupingSetsBoth(run, referenceSelect("SELECT sum(v) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}

func TestReferenceQueryAggregateExclusionPrefixEffects(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(v INT)", 0))
		// Malformed in-scope suffixes reject the whole input before the prefix
		// INSERT, through direct execution and actual Prepare.
		for _, suffix := range []string{
			"SELECT count(*) WHERE",
			"SELECT sum(v) FROM t WHERE",
			"SELECT v FROM t GROUP BY ROLLUP(",
			"SELECT v FROM t GROUP BY CUBE(",
			"SELECT v FROM t GROUP BY GROUPING SETS(",
			"SELECT v FROM t GROUP BY (",
			"SELECT v FROM t GROUP BY DISTINCT",
			"SELECT v FROM t GROUP BY ALL",
		} {
			t.Run(suffix, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
				q := "INSERT INTO t VALUES(1);" + suffix
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: referencePosition(utf8.RuneCountInString(q) + 1)})
				groupingSetsAnalysisError(run, s)
				run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
			})
		}
		groupingSetsAnalysisError(run, groupingSetsError("SELECT v FROM t GROUP BY ()", "42803", "column \"t.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v FROM"))
		// Unrelated window-frame exclusion is unchanged and native-only.
		if !backend.pg {
			q := "INSERT INTO t VALUES(1);SELECT count(*) OVER (ROWS UNBOUNDED PRECEDING) FROM t"
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceError(q, "0A000"))
			queryAggregatePrepareRejection(t, p, q, "0A000")
			run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		}
	})
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
