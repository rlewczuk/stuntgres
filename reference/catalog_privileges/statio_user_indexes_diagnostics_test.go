package reference_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatioUserIndexesDiagnostics(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_user_sentinel(k integer)")
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO statio_user_sentinel VALUES(99)", 1))
		for _, populated := range []bool{false, true} {
			if populated {
				statioExec(t, p, b, c, "CREATE INDEX statio_user_sentinel_i ON statio_user_sentinel(k)")
			}
			before := statioRows(t, p, b, c, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_sentinel'")
			for _, tc := range []struct{ q, verb, action, gerund string }{
				{"INSERT INTO pg_statio_user_indexes DEFAULT VALUES", "insert into", "INSERT", "inserting into"},
				{"INSERT INTO pg_statio_user_indexes(relid) VALUES(1)", "insert into", "INSERT", "inserting into"},
				{"INSERT INTO pg_statio_user_indexes(relid) SELECT relid FROM pg_statio_user_indexes WHERE false", "insert into", "INSERT", "inserting into"},
				{"INSERT INTO pg_statio_user_indexes(relid) SELECT relid FROM pg_statio_user_indexes", "insert into", "INSERT", "inserting into"},
				{"UPDATE pg_statio_user_indexes SET idx_blks_hit=0 WHERE false", "update", "UPDATE", "updating"},
				{"UPDATE pg_statio_user_indexes SET idx_blks_hit=0 WHERE relname='statio_user_sentinel'", "update", "UPDATE", "updating"},
				{"DELETE FROM pg_statio_user_indexes WHERE false", "delete from", "DELETE", "deleting from"},
				{"DELETE FROM pg_statio_user_indexes", "delete from", "DELETE", "deleting from"},
				{"TRUNCATE pg_statio_user_indexes", "", "", ""},
				{"SELECT stats_reset FROM pg_statio_user_indexes", "column", "", ""},
			} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + tc.verb + " view \"pg_statio_all_indexes\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."}
				if tc.verb == "" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "\"pg_statio_user_indexes\" is not a table"}
				}
				if tc.verb == "column" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"stats_reset\" does not exist", Position: "8"}
				}
				for _, qualified := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						q := tc.q
						if qualified {
							q = strings.ReplaceAll(q, "pg_statio_user_indexes", "pg_catalog.pg_statio_user_indexes")
						}
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						s.SelectRows = tc.verb == "column"
						s.ErrorStage = "execute"
						if prepared && tc.verb != "" {
							s.ErrorStage = "prepare"
						}
						vacuumRun(t, p, b, c, s)
						if got := statioRows(t, p, b, c, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_sentinel'"); !reflect.DeepEqual(got, before) {
							t.Fatal("rejected mutation changed facts", got, before)
						}
					}
				}
			}
			statioExec(t, p, b, c, "SET enable_indexscan=off", "SET enable_bitmapscan=off")
			vacuumRun(t, p, b, c, fixture.Select("SELECT k FROM statio_user_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}
		pool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer copyConn.Close()
		const copySQL = "COPY pg_catalog.pg_statio_user_indexes FROM STDIN"
		wantCopy := fixture.ControlsReferenceDiagnostic("42809", "cannot copy to view \"pg_statio_user_indexes\"")
		wantCopy.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		_, err = fixture.CopyFrom(p.Ctx, copyConn, b.PG, copySQL, strings.NewReader(""))
		fixture.AssertDiagnostic(t, err, &wantCopy)
		stmt, err := copyConn.PrepareContext(p.Ctx, copySQL)
		if b.PG {
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.ExecContext(p.Ctx)
			stmt.Close()
			fixture.AssertDiagnostic(t, err, &wantCopy)
		} else {
			if stmt != nil {
				stmt.Close()
				t.Fatal("streamless COPY admitted")
			}
			boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
			fixture.AssertDiagnostic(t, err, &boundary)
			_, err = copyConn.ExecContext(p.Ctx, copySQL)
			fixture.AssertDiagnostic(t, err, &boundary)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, "SELECT * FROM pg_statio_user_indexes")
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled read exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary cancellation", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation acquired SQLSTATE", state.SQLState())
		}
	})
}
