package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func bgwriterMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_bgwriter"`, "UPDATE": `cannot update view "pg_stat_bgwriter"`, "DELETE": `cannot delete from view "pg_stat_bgwriter"`}[operation]
	want := bgwriterDiagnostic("55000", message)
	want.Detail = "Views that do not select from a single table or view are not automatically updatable."
	want.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."
	return want
}

func TestReferencePGStatBGWriterReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE bgwriter_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO bgwriter_sentinel VALUES(37)", 1)
		before := bgwriterGet(t, p, b, c)
		held := bgwriterHeld(t, p, c, bgwriterSQL)
		defer held.Statement.Close()
		old, err := c.QueryContext(p.Ctx, bgwriterSQL)
		if err != nil {
			t.Fatal(err)
		}
		// pq streams on this physical connection; consume it before further SQL.
		if b.PG {
			bgwriterSameReset(t, before, bgwriterRead(t, old, true))
		} else {
			defer old.Close()
		}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_bgwriter(buffers_clean) VALUES(1)", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_bgwriter SET buffers_clean=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_bgwriter SET buffers_clean=1 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_bgwriter", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_bgwriter WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_bgwriter", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := bgwriterMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = bgwriterDiagnostic("42809", `"pg_stat_bgwriter" is not a table`)
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				bgwriterSameReset(t, before, bgwriterGet(t, p, b, held))
				bgwriterSentinel(t, p, b, c)
			}
		}
		for _, tc := range []struct {
			query, code, message string
			position int
		}{
			{"UPDATE pg_stat_bgwriter SET missing=1", "42703", `column "missing" of relation "pg_stat_bgwriter" does not exist`, 29},
			{"UPDATE pg_stat_bgwriter SET", "42601", "syntax error at end of input", 28},
		} {
			for _, prepared := range []bool{false, true} {
				want := bgwriterDiagnostic(tc.code, tc.message)
				want.Position = fixture.Position(tc.position)
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				bgwriterSameReset(t, before, bgwriterGet(t, p, b, held))
				bgwriterSentinel(t, p, b, c)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_bgwriter(buffers_clean) FROM STDIN", strings.NewReader("1\n"))
		want := bgwriterDiagnostic("42809", `cannot copy to view "pg_stat_bgwriter"`)
		want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		fixture.AssertDiagnostic(t, err, &want)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published effects", result)
		}
		bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
		bgwriterSameReset(t, before, bgwriterGet(t, p, b, held))
		bgwriterSentinel(t, p, b, c)
		if !b.PG {
			bgwriterSameReset(t, before, bgwriterRead(t, old, false))
		}
	})
}