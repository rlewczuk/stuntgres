package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The strict addition inside an otherwise nonstrict SQL OVERLAPS wrapper must
// not run ahead of a denied outer invocation. Granting that invocation exposes
// the independent timestamp overflow, including source-empty constant planning.
func TestReferenceObjectGrantRoutineBodyOverlapsPrecedence(t *testing.T) {
	objectGrantScenario(t, []string{"og_overlap_priority"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		target := "pg_catalog.overlaps(timestamp without time zone,interval,timestamp without time zone,timestamp without time zone)"
		defer objectGrantRoutineCleanup(t, p, c, []string{target})
		run(fixture.Exec("CREATE SCHEMA og_overlap_priority", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_overlap_priority TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_overlap_priority.source(b TIMESTAMP,c TIMESTAMP)", 0))
		run(fixture.Exec("INSERT INTO og_overlap_priority.source VALUES(TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-02')", 1))
		run(fixture.Exec("GRANT SELECT ON og_overlap_priority.source TO og_object_reader", 0))
		query := "SELECT (TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (b,c) AS v FROM og_overlap_priority.source"
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				step := fixture.ImplicitError(query+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for function overlaps"})
				step.SelectRows, step.ErrorStage = true, "execute"
				run(step)
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				step := fixture.ImplicitError(query+suffix, fixture.Diagnostic{Code: "22008", Message: "timestamp out of range", Context: `SQL function "overlaps" during inlining`})
				step.SelectRows, step.ErrorStage = true, "execute"
				run(step)
			}
		})
	})
}
