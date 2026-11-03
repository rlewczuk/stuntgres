package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func statActivityMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_activity"`, "UPDATE": `cannot update view "pg_stat_activity"`, "DELETE": `cannot delete from view "pg_stat_activity"`}[operation]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."}
}

func TestReferencePGStatActivityReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		activityExec(t, p, peer, "SET application_name='unchanged activity'")
		activityExec(t, p, o, "CREATE TABLE activity_readonly_sentinel(v integer)")
		activityExec(t, p, o, "INSERT INTO activity_readonly_sentinel VALUES(37)")
		before := statActivityGet(t, p, o, pid, true)
		selectStmt, err := o.PrepareContext(p.Ctx, statActivitySelect)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		for _, tc := range []struct {
			query, operation string
			args             []any
		}{
			{"INSERT INTO pg_catalog.pg_stat_activity(application_name) VALUES('x')", "INSERT", nil},
			{"UPDATE pg_catalog.pg_stat_activity SET application_name='x' WHERE pid=$1", "UPDATE", []any{pid}},
			{"DELETE FROM pg_catalog.pg_stat_activity WHERE pid=$1", "DELETE", []any{pid}},
			{"UPDATE pg_catalog.pg_stat_activity SET application_name='x' WHERE FALSE", "UPDATE", nil},
			{"DELETE FROM pg_catalog.pg_stat_activity WHERE FALSE", "DELETE", nil},
			{"TRUNCATE pg_catalog.pg_stat_activity", "TRUNCATE", nil},
		} {
			for _, prepared := range []bool{false, true} {
				want := statActivityMutationDiagnostic(tc.operation)
				if tc.operation == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_activity" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want, tc.args...)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
				statActivityClear(t, p, o)
				rows, err := selectStmt.QueryContext(p.Ctx, pid)
				if err != nil {
					t.Fatal(err)
				}
				after, ok := statActivityRead(t, rows)
				if !ok {
					t.Fatal("mutation removed session")
				}
				for i := range before {
					if before.text(i) != after.text(i) {
						t.Fatalf("view mutation changed peer %s: %v -> %v", statActivityNames[i], before[i], after[i])
					}
				}
				fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Select("SELECT v FROM activity_readonly_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_activity(application_name) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_activity"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published command", result)
		}
		if r := statActivityGet(t, p, o, pid, true); r.text(6) != "unchanged activity" {
			t.Fatal("COPY changed activity", r)
		}
	})
}
