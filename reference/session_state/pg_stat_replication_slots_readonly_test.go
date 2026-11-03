package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE TABLE replication_slots_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO replication_slots_sentinel VALUES(7)", 1)
		held := prefetchHeld(t, p, c, replicationSlotsSQL)
		defer held.Statement.Close()
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_replication_slots(slot_name) VALUES('fake')", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_replication_slots SET total_txns=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_replication_slots SET total_txns=1 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_replication_slots", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_replication_slots WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_replication_slots", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := replicationSlotsMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = activityDiagnostic("42809", `"pg_stat_replication_slots" is not a table`, 0)
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				replicationSlotsEmpty(t, p, b, c)
				replicationSlotsEmpty(t, p, b, held)
				replicationSlotsSentinel(t, p, b, c)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_replication_slots(slot_name) FROM STDIN", strings.NewReader("fake\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_replication_slots"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published count/tag", result)
		}
		replicationSlotsEmpty(t, p, b, c)
		replicationSlotsEmpty(t, p, b, held)
		replicationSlotsSentinel(t, p, b, c)
	})
}
