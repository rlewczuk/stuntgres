package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCountExpressionCapabilityBoundaries(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT count(*)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s := fixture.GroupingSetsBag("SELECT count(*) OVER () FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(3)}, {int64(3)}})
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
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
				fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag(tc.q, []string{"count"}, []string{"INT8"}, tc.rows))
			})
		}
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT sum(v) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}

func TestReferenceQueryAggregateExclusionPrefixEffects(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE t(v INT)", 0))
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
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
				q := "INSERT INTO t VALUES(1);" + suffix
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(utf8.RuneCountInString(q) + 1)})
				fixture.GroupingSetsAnalysisError(run, s)
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
			})
		}
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError("SELECT v FROM t GROUP BY ()", "42803", "column \"t.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v FROM"))
		// The supported ROWS frame executes in a direct message; prepared and
		// argument-bearing messages reject multiple commands before analysis.
		q := "INSERT INTO t VALUES(1);SELECT count(*) OVER (ROWS UNBOUNDED PRECEDING) FROM t"
		run(fixture.Exec(q, 1))
		sentinel := fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
		run(sentinel)
		batch := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
		batch.ErrorStage = "prepare"
		run(fixture.Prepared(batch))
		run(sentinel)
		batch.Args = []any{1}
		batch.ErrorStage = "execute"
		run(batch)
		run(sentinel)
	})
}
