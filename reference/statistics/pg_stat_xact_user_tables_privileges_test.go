package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xut_reader"})
	fixture.SchemaReferenceCleanup(t, "xut_private")
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := c.ExecContext(ctx, "REVOKE SELECT (relname) ON pg_catalog.pg_stat_xact_user_tables FROM xut_reader"); err != nil {
				t.Error("restore column grant", err)
			}
			for _, view := range []string{"pg_class", "pg_stat_xact_user_tables", "pg_stat_xact_all_tables"} {
				if _, err := c.ExecContext(ctx, "GRANT SELECT ON pg_catalog."+view+" TO PUBLIC"); err != nil {
					t.Error("restore PUBLIC grant", err)
				}
			}
		}()
		fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA xut_private", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE ALL ON SCHEMA xut_private FROM PUBLIC", 0)
		tableStatCreate(t, p, b, c, "xut_private.xut_privileges")
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE xut_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		pool := fixture.RoleAttributesPool(t, p, b, "xut_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		stmt, err := reader.PrepareContext(p.Ctx, xactUserSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		const columnSQL = "SELECT relname FROM pg_catalog.pg_stat_xact_user_tables WHERE relname='xut_privileges'"
		columnStmt, err := reader.PrepareContext(p.Ctx, columnSQL)
		if err != nil { t.Fatal(err) }
		defer columnStmt.Close()
		positive := func() {
			t.Helper()
			direct := xactUserGet(t, p, reader, "xut_privileges")
			var held xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, "xut_privileges").Scan(held.destinations()...); err != nil || direct.ID <= 0 || held.ID != direct.ID || held.Schema != "xut_private" {
				t.Fatal("unredacted inaccessible base identity", direct, held, err)
			}
			xactTableCounts(t, direct, 0, 0, 0)
			xactTableCounts(t, held, 0, 0, 0)
		}
		positive()
		fixture.StatisticsExec(t, p, b, c, "GRANT USAGE ON SCHEMA xut_private TO xut_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT INSERT ON xut_private.xut_privileges TO xut_reader", 0)
		// Keep the held reader a genuine peer. Its earlier reads may have
		// established a publication throttle, so reusing it as a writer and
		// assuming ROLLBACK cleared its pending batch is not an isolation proof.
		roleBackend := b
		roleBackend.DB = pool
		roleWriter, retireRoleWriter := fixture.StatisticsReportingConn(t, p, roleBackend)
		fixture.StatisticsExec(t, p, b, roleWriter, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, roleWriter, "INSERT INTO xut_private.xut_privileges VALUES(1,NULL),(2,'b')", 2)
		xactTableCounts(t, xactUserGet(t, p, roleWriter, "xut_privileges"), 2, 0, 0)
		positive()
		xactTableCounts(t, xactUserGet(t, p, c, "xut_privileges"), 0, 0, 0)
		xactTableEnd(t, p, b, roleWriter, "ROLLBACK")
		retireRoleWriter()
		fixture.StatisticsExec(t, p, b, c, "REVOKE INSERT ON xut_private.xut_privileges FROM xut_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE USAGE ON SCHEMA xut_private FROM xut_reader", 0)
		positive()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_class FROM PUBLIC", 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SELECT oid FROM pg_catalog.pg_class WHERE false", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_class"})
			step.Prepared = prepared
			step.SelectRows = true
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, step)
		}
		positive()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_class TO PUBLIC", 0)
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		defer retire()
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO xut_private.xut_privileges VALUES(1,NULL),(2,'b')", 2)
		xactTableCounts(t, xactUserGet(t, p, w, "xut_privileges"), 2, 0, 0)
		xactTableCounts(t, xactUserGet(t, p, reader, "xut_privileges"), 0, 0, 0)
		falseSQL := "SELECT * FROM pg_catalog.pg_stat_xact_user_tables WHERE FALSE"
		falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer falseStmt.Close()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_xact_user_tables FROM PUBLIC", 0)
		denial := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_xact_user_tables"}
		rows, err := stmt.QueryContext(p.Ctx, "xut_privileges")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		held := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
		step := fixture.ErrorDiagnostic(falseSQL, denial)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		for _, q := range []string{falseSQL, "SELECT * FROM pg_catalog.pg_stat_xact_user_tables WHERE relname='xut_privileges'"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, denial)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
			}
		}
		fresh, err := reader.PrepareContext(p.Ctx, xactUserSelect)
		if err != nil {
			t.Fatal("Prepare must not capture SELECT authority", err)
		}
		rows, err = fresh.QueryContext(p.Ctx, "xut_privileges")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		fresh.Close()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_xact_user_tables TO PUBLIC", 0)
		positive()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_xact_all_tables FROM PUBLIC", 0)
		positive()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_xact_all_tables TO PUBLIC", 0)
		xactTableEnd(t, p, b, w, "ROLLBACK")

		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_xact_user_tables FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT (relname) ON pg_catalog.pg_stat_xact_user_tables TO xut_reader", 0)
		for _, prepared := range []bool{false, true} {
			allowed := fixture.Select(columnSQL, []string{"relname"}, []string{"NAME"}, [][]any{{"xut_privileges"}})
			allowed.Prepared = prepared
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, allowed)
			for _, q := range []string{"SELECT seq_scan FROM pg_catalog.pg_stat_xact_user_tables WHERE FALSE", falseSQL, "SELECT * FROM pg_catalog.pg_stat_xact_user_tables WHERE relname='xut_privileges'"} {
				denied := fixture.ErrorDiagnostic(q, denial)
				denied.Prepared = prepared
				denied.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, denied)
			}
		}
		columnHeld := fixture.HeldStatement{Executor: reader, Statement: columnStmt, SQL: columnSQL}
		fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, fixture.Select(columnSQL, []string{"relname"}, []string{"NAME"}, [][]any{{"xut_privileges"}}))
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT (relname) ON pg_catalog.pg_stat_xact_user_tables FROM xut_reader", 0)
		columnDenied := fixture.ErrorDiagnostic(columnSQL, denial)
		columnDenied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, columnDenied)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_xact_user_tables TO PUBLIC", 0)
		fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, fixture.Select(columnSQL, []string{"relname"}, []string{"NAME"}, [][]any{{"xut_privileges"}}))

	})
}

func TestReferencePGStatXactUserTablesReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_readonly")
		xactUserSeed(t, p, b, "xut_readonly")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		before := xactUserGet(t, p, c, "xut_readonly")
		maintenance := tableStatGet(t, p, c, "xut_readonly")
		for _, tc := range []struct{ q, op string }{
			{"INSERT INTO pg_stat_xact_user_tables(relname) VALUES('x')", "INSERT"},
			{"INSERT INTO pg_stat_xact_user_tables(relname) SELECT 'x' WHERE FALSE", "INSERT"},
			{"UPDATE pg_stat_xact_user_tables SET seq_scan=9 WHERE relname='xut_readonly'", "UPDATE"},
			{"UPDATE pg_stat_xact_user_tables SET seq_scan=9 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_stat_xact_user_tables WHERE relname='xut_readonly'", "DELETE"},
			{"DELETE FROM pg_stat_xact_user_tables WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_stat_xact_user_tables", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := xactTableDiagnostic(tc.op)
				if tc.op == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_xact_user_tables" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.q, want)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if prepared && tc.op != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if got := xactUserGet(t, p, c, "xut_readonly"); got != before {
					t.Fatal("rejected mutation changed USER", got, before)
				}
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_xact_user_tables(relname) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_xact_user_tables"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("rejected COPY accepted rows/tag", result)
		}
		if got := xactUserGet(t, p, c, "xut_readonly"); got != before {
			t.Fatal("COPY changed USER", got, before)
		}
		if got := tableStatGet(t, p, c, "xut_readonly"); !got.equal(maintenance) {
			t.Fatal("DML changed cumulative state", got, maintenance)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM public.xut_readonly ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
	})
}
