package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func walMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_wal"`, "UPDATE": `cannot update view "pg_stat_wal"`, "DELETE": `cannot delete from view "pg_stat_wal"`}[operation]
	want := archiverDiagnostic("55000", message)
	want.Detail = "Views that do not select from a single table or view are not automatically updatable."
	want.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."
	return want
}

func TestReferencePGStatWALReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE wal_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO wal_sentinel VALUES(37)", 1)
		before := walGet(t, p, b, c)
		held := archiverHeld(t, p, c, walSQL)
		defer held.Statement.Close()
		old, err := c.QueryContext(p.Ctx, walSQL)
		if err != nil {
			t.Fatal(err)
		}
		// pq streams on this physical connection; consume it before further SQL.
		if b.PG {
			walSame(t, before, walRead(t, old, true))
		} else {
			defer old.Close()
		}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_wal(wal_records) VALUES(1)", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_wal(wal_bytes) VALUES(9007199254740993::numeric)", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_wal SET wal_records=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_wal SET wal_records=1 WHERE FALSE", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_wal SET wal_bytes=9007199254740993::numeric WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_wal", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_wal WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_wal", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := walMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = archiverDiagnostic("42809", `"pg_stat_wal" is not a table`)
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.StatisticsExec(t, p, b, c, "SAVEPOINT rejected_wal_mutation", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT rejected_wal_mutation", 0)
				if at := archiverSnapshotTime(t, p, c); at.Valid {
					t.Fatal("rejected mutation created cumulative snapshot", at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
				walSame(t, before, walGet(t, p, b, held))
				walSentinel(t, p, b, c)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_wal(wal_records) FROM STDIN", strings.NewReader("1\n"))
		want := archiverDiagnostic("42809", `cannot copy to view "pg_stat_wal"`)
		want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		fixture.AssertDiagnostic(t, err, &want)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published effects", result)
		}
		walSame(t, before, walGet(t, p, b, c))
		walSame(t, before, walGet(t, p, b, held))
		walSentinel(t, p, b, c)
		if !b.PG {
			walSame(t, before, walRead(t, old, false))
		}
	})
}
