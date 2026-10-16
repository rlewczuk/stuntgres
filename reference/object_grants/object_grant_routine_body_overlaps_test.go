package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineBodyOverlapsDemand(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		addition := "pg_catalog.timestamp_pl_interval(timestamp without time zone,interval)"
		endpoint := "pg_catalog.overlaps(timestamp without time zone,timestamp without time zone,timestamp without time zone,timestamp without time zone)"
		defer objectGrantRoutineCleanup(t, p, c, []string{addition, endpoint})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_overlap_source(a TIMESTAMP,b TIMESTAMP,c TIMESTAMP)", 0))
		run(fixture.Exec("INSERT INTO og_overlap_source VALUES(TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04')", 1))
		run(fixture.Exec("GRANT SELECT ON og_overlap_source TO og_object_reader", 0))
		nullDuration := "SELECT (a,NULL::INTERVAL) OVERLAPS (b,c) AS v FROM og_overlap_source"
		allNull := "SELECT (NULL::TIMESTAMP,NULL::INTERVAL) OVERLAPS (b,c) AS v FROM og_overlap_source"
		partial := "SELECT (TIMESTAMP '2001-01-01',INTERVAL '2 days') OVERLAPS (b,c) AS v FROM og_overlap_source"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(nullDuration, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Select(allNull, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			run(fixture.Select(partial, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+addition+" FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			// Strict NULL removes the addition, not the nonstrict endpoint call.
			run(fixture.Select(nullDuration, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Select(allNull, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			for _, suffix := range []string{"", " WHERE FALSE"} {
				// A constant pair still computes its addition while inlining,
				// even though the other pair is row-dependent or source-empty.
				s := fixture.ImplicitError(partial+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for function timestamp_pl_interval", Context: `SQL function "overlaps" during inlining`})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+addition+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(partial, []string{"v"}, []string{"BOOL"}, [][]any{{true}})) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+addition+" FROM og_object_reader", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+endpoint+" FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range []string{nullDuration, allNull, nullDuration + " WHERE FALSE"} {
				fixture.RoleGrantPermissionDenied(run, q, "function", "overlaps")
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+endpoint+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(nullDuration, []string{"v"}, []string{"BOOL"}, [][]any{{true}})) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+endpoint+" FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, allNull, "function", "overlaps") })
	})
}
