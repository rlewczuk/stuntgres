package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSysTablesPrivileges(t *testing.T) {
	tableStatPrivileges(t, tableStatFamily{system: true})
}

func tableStatPrivileges(t *testing.T, f tableStatFamily) {
	fixture.RoleReferenceCleanup(t, []string{"sst_reader"})
	schema := "information_schema"
	if f.user {
		schema = "sut_private"
		fixture.SchemaReferenceCleanup(t, schema)
	}
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"GRANT USAGE ON SCHEMA " + schema + " TO PUBLIC", "GRANT SELECT ON pg_catalog." + f.view() + " TO PUBLIC", "GRANT SELECT ON pg_catalog.pg_stat_all_tables TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("restore public ACL", err)
				}
			}
		}()
		if f.user {
			fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA "+schema, 0)
			fixture.StatisticsExec(t, p, b, c, "GRANT USAGE ON SCHEMA "+schema+" TO PUBLIC", 0)
		}
		f.create(t, p, b, c, schema+".sst_privileges")
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE sst_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		pool := fixture.RoleAttributesPool(t, p, b, "sst_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency=none", 0)
		stmt, err := reader.PrepareContext(p.Ctx, f.query())
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		positive := func(want int64) {
			t.Helper()
			direct := f.get(t, p, reader, "sst_privileges")
			var held tableStatRow
			if err := stmt.QueryRowContext(p.Ctx, "sst_privileges").Scan(held.destinations()...); err != nil {
				t.Fatal(err)
			}
			if !direct.equal(held) || direct.Inserts != want || direct.Schema != schema || direct.ID <= 0 {
				t.Fatal("unredacted system facts", direct, held, want)
			}
		}
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT n FROM "+schema+".sst_privileges", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table sst_privileges"}))
		positive(0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE USAGE ON SCHEMA "+schema+" FROM PUBLIC", 0)
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT n FROM "+schema+".sst_privileges", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema " + schema, Position: "15"}))
		positive(0)
		w, retire := f.reporting(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+schema+".sst_privileges VALUES(1,NULL),(2,'b')", 2)
		retire()
		f.await(t, p, b, "sst_privileges", "inaccessible executed inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
		positive(2)
		falseSQL := "SELECT * FROM pg_catalog." + f.view() + " WHERE FALSE"
		falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer falseStmt.Close()
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog."+f.view()+" FROM PUBLIC", 0)
		denial := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view " + f.view()}
		rows, err := stmt.QueryContext(p.Ctx, "sst_privileges")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		held := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
		step := fixture.ErrorDiagnostic(falseSQL, denial)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		for _, q := range []string{falseSQL, "SELECT * FROM pg_catalog." + f.view() + " WHERE relname='sst_privileges'"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, denial)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
			}
		}
		fresh, err := reader.PrepareContext(p.Ctx, f.query())
		if err != nil {
			t.Fatal("SELECT authority captured at prepare", err)
		}
		rows, err = fresh.QueryContext(p.Ctx, "sst_privileges")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		fresh.Close()
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog."+f.view()+" TO PUBLIC", 0)
		positive(2)
		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_all_tables FROM PUBLIC", 0)
		positive(2)
	})
}
