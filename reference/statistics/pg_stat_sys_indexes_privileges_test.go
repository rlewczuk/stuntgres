package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSysIndexesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"six_reader"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, view := range []string{"pg_stat_sys_indexes", "pg_stat_all_indexes"} {
				if _, err := c.ExecContext(ctx, "GRANT SELECT ON pg_catalog."+view+" TO PUBLIC"); err != nil {
					t.Error("restore monitoring view grant", err)
				}
			}
		}()
		sysIndexCreate(t, p, b, c, "information_schema", "six_privileges")
		expected := sysIndexAccesses(t, p, b, c, "six_privileges")
		if !b.PG && (!expected.Last.Valid || expected.Scans == 0 || expected.Fetches == 0) {
			t.Fatal("native unredacted positive witness missing", expected)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE six_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		pool := fixture.RoleAttributesPool(t, p, b, "six_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency=none", 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SELECT n FROM information_schema.six_privileges WHERE FALSE", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table six_privileges"})
			step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, step)
		}
		stmt, err := reader.PrepareContext(p.Ctx, sysIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		positive := func() {
			t.Helper()
			direct := sysIndexGet(t, p, reader, expected.Name)
			held, err := sysIndexHeld(t, p, stmt, expected.Name)
			if err != nil || !direct.equal(expected) || !held.equal(expected) {
				t.Fatal("parent ACL must not redact identities, counters or instant", direct, held, expected, err)
			}
		}
		positive()
		const falseSQL = "SELECT * FROM pg_catalog.pg_stat_sys_indexes WHERE FALSE"
		falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer falseStmt.Close()
		falseHeld := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
		fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, fixture.Select(falseSQL, indexStatNames, indexStatTypes, nil))
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_sys_indexes FROM PUBLIC", 0)
		denial := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_sys_indexes"}
		rows, err := stmt.QueryContext(p.Ctx, expected.Name)
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		step := fixture.ErrorDiagnostic(falseSQL, denial)
		step.ErrorStage, step.SelectRows = "execute", true
		fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, step)
		for _, query := range []string{falseSQL, "SELECT * FROM pg_catalog.pg_stat_sys_indexes WHERE indexrelname='six_privileges_hash'"} {
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(query, denial)
				step.Prepared, step.ErrorStage, step.SelectRows = prepared, "execute", true
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, step)
			}
		}
		fresh, err := reader.PrepareContext(p.Ctx, sysIndexSelect)
		if err != nil {
			t.Fatal("Prepare must not capture SELECT authority", err)
		}
		rows, err = fresh.QueryContext(p.Ctx, expected.Name)
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		fresh.Close()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_sys_indexes TO PUBLIC", 0)
		positive()
		fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, fixture.Select(falseSQL, indexStatNames, indexStatTypes, nil))
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_all_indexes FROM PUBLIC", 0)
		positive() // default dependency privileges belong to the view owner
		fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, fixture.Select(falseSQL, indexStatNames, indexStatTypes, nil))
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_all_indexes TO PUBLIC", 0)
	})
}

func TestReferencePGStatSysIndexesReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "information_schema", "six_readonly")
		before := sysIndexAccesses(t, p, b, c, "six_readonly")
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		stmt, err := c.PrepareContext(p.Ctx, sysIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		unchanged := func() {
			t.Helper()
			held, err := sysIndexHeld(t, p, stmt, before.Name)
			if err != nil || !held.equal(before) || !sysIndexGet(t, p, c, before.Name).equal(before) || !indexStatGet(t, p, c, before.Name).equal(before) {
				t.Fatal("rejected mutation changed SYS/ALL identity or facts", held, before, err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM information_schema.six_readonly ORDER BY n,payload", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "a"}, {int64(2), "b"}, {int64(3), "z"}, {nil, nil}}))
		}
		for _, tc := range []struct{ query, op string }{
			{"INSERT INTO pg_catalog.pg_stat_sys_indexes(relname) VALUES('x')", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_sys_indexes(relname) SELECT 'x' WHERE FALSE", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_sys_indexes SET idx_scan=9 WHERE indexrelname='six_readonly_hash'", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_sys_indexes SET idx_scan=9 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_sys_indexes WHERE indexrelname='six_readonly_hash'", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_sys_indexes WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_sys_indexes", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := indexStatMutationDiagnostic(tc.op)
				if tc.op == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_sys_indexes" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.op != "TRUNCATE" {
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_sys_indexes(relname) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_sys_indexes"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("rejected COPY returned count/tag", result)
		}
		unchanged()
	})
}
