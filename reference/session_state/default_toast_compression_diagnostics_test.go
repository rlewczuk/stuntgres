package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDefaultToastCompressionDiagnosticsAndPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"toast_compression_reader"})
	toastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := toastRun(t, p, b, c)
		toastInsertPair(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			for _, value := range []string{"bogus", "", "lz", " pglz "} {
				q := "SET default_toast_compression='" + value + "'"
				s := toastInvalid(q, value)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					statement, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("invalid value must fail at execution, not prepare", err)
					}
					run(toastText(b.PG, "SHOW default_toast_compression", toastSetting, "lz4"))
					s.Prepared = false
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}, b.Recorder, s)
					statement.Close()
				} else {
					run(s)
				}
				for _, unchanged := range toastState(b.PG, "lz4", "session") {
					run(unchanged)
				}
				run(toastRows("toast_default_case", 2))
				run(toastPolicy("toast_default_case", ""))
			}
			s := toastFailure("SET default_toast_compression=pglz,lz4", "22023", "SET default_toast_compression takes only one argument")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(toastText(b.PG, "SHOW default_toast_compression", toastSetting, "lz4"))
			run(toastRows("toast_default_case", 2))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			txrun := toastRun(t, p, b, tx)
			txrun(fixture.Exec("SAVEPOINT toast_invalid", 0))
			s = toastInvalid("SET default_toast_compression=bogus", "bogus")
			s.Prepared, s.ErrorStage = prepared, "execute"
			txrun(s)
			txrun(fixture.Exec("ROLLBACK TO toast_invalid", 0))
			for _, restored := range toastState(b.PG, "lz4", "session") {
				txrun(restored)
			}
			txrun(toastRows("toast_default_case", 2))
			txrun(fixture.Exec("RELEASE toast_invalid", 0))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("CREATE ROLE toast_compression_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO toast_compression_reader", 0))
		roleConn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer roleConn.Close()
		roleRun := toastRun(t, p, b, roleConn)
		roleRun(fixture.Exec("SET ROLE toast_compression_reader", 0))
		defer func() {
			if _, err := roleConn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error("restore role", err)
			}
		}()
		for _, prepared := range []bool{false, true} {
			for _, value := range []string{"pglz", "lz4"} {
				s := fixture.Exec("SET default_toast_compression="+value, 0)
				s.Prepared = prepared
				roleRun(s)
				for _, visible := range toastState(b.PG, value, "session") {
					visible.Prepared = prepared
					roleRun(visible)
				}
			}
			q := "SELECT pg_column_compression(payload) AS compression FROM toast_default_case WHERE id=1"
			s := toastFailure(q, "42501", "permission denied for table toast_default_case")
			s.Prepared = prepared
			roleRun(s)
			roleRun(toastText(b.PG, "SHOW default_toast_compression", toastSetting, "lz4"))
			run(toastRows("toast_default_case", 2))
		}
		run(fixture.Exec("GRANT SELECT ON toast_default_case TO toast_compression_reader", 0))
		for _, prepared := range []bool{false, true} {
			s := toastDemand(b.PG, "SELECT pg_column_compression(payload) AS compression FROM toast_default_case WHERE id=1", "pglz")
			s.Prepared = prepared
			roleRun(s)
			roleRun(toastText(b.PG, "SHOW default_toast_compression", toastSetting, "lz4"))
			roleRun(toastRows("toast_default_case", 2))
		}
		run(toastRows("toast_default_case", 2))
		run(toastPolicy("toast_default_case", ""))
	})
}
