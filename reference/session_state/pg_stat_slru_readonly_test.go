package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func slruMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_slru"`, "UPDATE": `cannot update view "pg_stat_slru"`, "DELETE": `cannot delete from view "pg_stat_slru"`}[operation]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."}
}

func TestReferencePGStatSLRUReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE TABLE slru_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO slru_sentinel VALUES(37)", 1)
		before := slruGet(t, p, b, c)
		held := slruHeld(t, p, c, slruSQL)
		defer held.Statement.Close()
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_slru(name) VALUES('invented')", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_slru SET blks_hit=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_slru SET blks_hit=1 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_slru", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_slru WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_slru", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := slruMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_slru" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				slruSame(t, before, slruGet(t, p, b, c))
				slruSame(t, before, slruGet(t, p, b, held))
				slruSentinel(t, p, b, c)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_slru(blks_hit) FROM STDIN", strings.NewReader("1\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_slru"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY accepted input/published a command", result)
		}
		slruSame(t, before, slruGet(t, p, b, c))
		slruSame(t, before, slruGet(t, p, b, held))
		slruSentinel(t, p, b, c)
	})
}
