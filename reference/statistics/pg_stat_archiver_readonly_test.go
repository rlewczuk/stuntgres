package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func archiverMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_archiver"`, "UPDATE": `cannot update view "pg_stat_archiver"`, "DELETE": `cannot delete from view "pg_stat_archiver"`}[operation]
	want := archiverDiagnostic("55000", message)
	want.Detail = "Views that do not select from a single table or view are not automatically updatable."
	want.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."
	return want
}

func TestReferencePGStatArchiverReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE archiver_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO archiver_sentinel VALUES(37)", 1)
		before := archiverGet(t, p, b, c)
		held := archiverHeld(t, p, c, archiverSQL)
		defer held.Statement.Close()
		old, err := c.QueryContext(p.Ctx, archiverSQL)
		if err != nil {
			t.Fatal(err)
		}
		// pq streams on this physical connection; consume it before further SQL.
		if b.PG {
			archiverSame(t, before, archiverRead(t, old, true))
		} else {
			defer old.Close()
		}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_archiver(archived_count) VALUES(1)", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_archiver SET archived_count=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_archiver SET archived_count=1 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_archiver", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_archiver WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_archiver", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := archiverMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = archiverDiagnostic("42809", `"pg_stat_archiver" is not a table`)
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				archiverSame(t, before, archiverGet(t, p, b, held))
				archiverSentinel(t, p, b, c)
			}
		}
		pool := p.Native
		if b.PG {
			pool = p.CopyPostgres
		}
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer copyConn.Close()
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_archiver(archived_count) FROM STDIN", strings.NewReader("1\n"))
		want := archiverDiagnostic("42809", `cannot copy to view "pg_stat_archiver"`)
		want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		fixture.AssertDiagnostic(t, err, &want)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published effects", result)
		}
		archiverSame(t, before, archiverGet(t, p, b, c))
		archiverSame(t, before, archiverGet(t, p, b, held))
		archiverSentinel(t, p, b, c)
		if !b.PG {
			archiverSame(t, before, archiverRead(t, old, false))
		}
	})
}
