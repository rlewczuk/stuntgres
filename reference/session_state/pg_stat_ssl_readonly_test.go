package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sslMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_ssl"`, "UPDATE": `cannot update view "pg_stat_ssl"`, "DELETE": `cannot delete from view "pg_stat_ssl"`}[operation]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."}
}

func TestReferencePGStatSSLReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE TABLE ssl_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO ssl_sentinel VALUES(37)", 1)
		a, other := statActivityConn(t, p, b), statActivityConn(t, p, b)
		apid, bpid := statActivityPID(t, p, a), statActivityPID(t, p, other)
		held := sslHeld(t, p, c, sslPairSQL)
		defer held.Statement.Close()
		unchanged := func() {
			sslPair(t, p, b, held, apid, bpid)
			fixture.StatisticsClearSnapshot(t, p, b, c)
			sslPair(t, p, b, c, apid, bpid)
			sslSentinel(t, p, b, c)
		}
		unchanged()
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_ssl(pid) VALUES(1)", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_ssl(pid) SELECT 1 WHERE FALSE", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_ssl SET ssl=TRUE", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_ssl SET ssl=TRUE WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_ssl", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_ssl WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_ssl", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := sslMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_ssl" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				unchanged()
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_ssl(pid) FROM STDIN", strings.NewReader("1\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_ssl"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("rejected SSL COPY accepted input", result)
		}
		unchanged()
	})
}
