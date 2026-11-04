package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactSysTablesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xst_reader"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, view := range []string{"pg_class", "pg_stat_xact_sys_tables", "pg_stat_xact_all_tables"} {
				if _, err := c.ExecContext(ctx, "GRANT SELECT ON pg_catalog."+view+" TO PUBLIC"); err != nil {
					t.Error("restore PUBLIC grant", err)
				}
			}
		}()
		xactSysCreate(t, p, b, c, "xst_privileges")
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE xst_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		pool := fixture.RoleAttributesPool(t, p, b, "xst_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		stmt, err := reader.PrepareContext(p.Ctx, xactSysSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		positive := func() {
			t.Helper()
			direct := xactSysGet(t, p, reader, "pg_class")
			var held xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, "pg_class").Scan(held.destinations()...); err != nil || direct.ID != 1259 || held.ID != 1259 || held.Schema != "pg_catalog" {
				t.Fatal("current unredacted catalog identity", direct, held, err)
			}
		}
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
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.xst_privileges VALUES(1,NULL),(2,'b')", 2)
		xactTableCounts(t, xactSysGet(t, p, w, "xst_privileges"), 2, 0, 0)
		xactTableCounts(t, xactSysGet(t, p, reader, "xst_privileges"), 0, 0, 0)
		falseSQL := "SELECT * FROM pg_catalog.pg_stat_xact_sys_tables WHERE FALSE"
		falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer falseStmt.Close()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_xact_sys_tables FROM PUBLIC", 0)
		denial := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_xact_sys_tables"}
		rows, err := stmt.QueryContext(p.Ctx, "pg_class")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		held := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
		step := fixture.ErrorDiagnostic(falseSQL, denial)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		for _, q := range []string{falseSQL, "SELECT * FROM pg_catalog.pg_stat_xact_sys_tables WHERE relid=1259"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, denial)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
			}
		}
		fresh, err := reader.PrepareContext(p.Ctx, xactSysSelect)
		if err != nil {
			t.Fatal("Prepare must not capture SELECT authority", err)
		}
		rows, err = fresh.QueryContext(p.Ctx, "pg_class")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		fresh.Close()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_xact_sys_tables TO PUBLIC", 0)
		positive()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_xact_all_tables FROM PUBLIC", 0)
		positive()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_xact_all_tables TO PUBLIC", 0)
		xactTableEnd(t, p, b, w, "ROLLBACK")
	})
}

func TestReferencePGStatXactSysTablesReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_readonly")
		xactSysSeed(t, p, b, "xst_readonly")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		before := xactSysGet(t, p, c, "xst_readonly")
		maintenance := tableStatGet(t, p, c, "xst_readonly")
		for _, tc := range []struct{ q, op string }{
			{"INSERT INTO pg_stat_xact_sys_tables(relname) VALUES('x')", "INSERT"},
			{"INSERT INTO pg_stat_xact_sys_tables(relname) SELECT 'x' WHERE FALSE", "INSERT"},
			{"UPDATE pg_stat_xact_sys_tables SET seq_scan=9 WHERE relname='xst_readonly'", "UPDATE"},
			{"UPDATE pg_stat_xact_sys_tables SET seq_scan=9 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_stat_xact_sys_tables WHERE relname='xst_readonly'", "DELETE"},
			{"DELETE FROM pg_stat_xact_sys_tables WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_stat_xact_sys_tables", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := xactTableDiagnostic(tc.op)
				if tc.op == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_xact_sys_tables" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.q, want)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if prepared && tc.op != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if got := xactSysGet(t, p, c, "xst_readonly"); got != before {
					t.Fatal("rejected mutation changed SYS", got, before)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_xact_sys_tables(relname) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_xact_sys_tables"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("rejected COPY accepted rows/tag", result)
		}
		if got := xactSysGet(t, p, c, "xst_readonly"); got != before {
			t.Fatal("COPY changed SYS", got, before)
		}
		if got := tableStatGet(t, p, c, "xst_readonly"); !got.equal(maintenance) {
			t.Fatal("DML changed cumulative state", got, maintenance)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM information_schema.xst_readonly ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
	})
}
