package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE TABLE replication_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO replication_sentinel VALUES(37)", 1)
		held := replicationHeld(t, p, c, replicationSQL)
		defer held.Statement.Close()
		for _, tc := range []struct {
			query, operation string
			args             []any
		}{
			{"INSERT INTO pg_catalog.pg_stat_replication(application_name) VALUES('x')", "INSERT", nil},
			{"UPDATE pg_catalog.pg_stat_replication SET application_name='x' WHERE pid=$1", "UPDATE", []any{int64(-1)}},
			{"DELETE FROM pg_catalog.pg_stat_replication WHERE pid=$1", "DELETE", []any{int64(-1)}},
			{"UPDATE pg_catalog.pg_stat_replication SET application_name='x' WHERE FALSE", "UPDATE", nil},
			{"DELETE FROM pg_catalog.pg_stat_replication WHERE FALSE", "DELETE", nil},
			{"TRUNCATE pg_catalog.pg_stat_replication", "TRUNCATE", nil},
		} {
			for _, prepared := range []bool{false, true} {
				want := replicationMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_replication" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want, tc.args...)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				replicationEmpty(t, p, b, held)
				replicationSentinel(t, p, b, c)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_replication(application_name) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_replication"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published command", result)
		}
		replicationEmpty(t, p, b, held)
		replicationSentinel(t, p, b, c)
	})
}
