package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetDatabasePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"database_reset_reader", "database_reset_group"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resetDatabaseExec(t, p, c, "CREATE ROLE database_reset_reader", "CREATE ROLE database_reset_group", "GRANT pg_monitor,pg_read_all_stats TO database_reset_reader", "CREATE TABLE reset_runtime_null(v oid)", "INSERT INTO reset_runtime_null VALUES(NULL)", "GRANT SELECT ON reset_runtime_null TO database_reset_reader")
		activityExec(t, p, c, "GRANT USAGE ON SCHEMA public TO database_reset_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		bootstrap := "postgres"
		if b.PG {
			bootstrap = "stuntgres"
		}
		for _, f := range resetDatabaseFunctions {
			formal, arg := "", ""
			if f.strict {
				formal = "oid"
				arg = "0::oid"
			}
			identity := "pg_catalog." + f.name + "(" + formal + ")"
			defer func() {
				c.ExecContext(context.Background(), "RESET ROLE")
				c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM database_reset_reader,database_reset_group,PUBLIC")
			}()
			var live, initial string
			if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&live); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT initprivs::text FROM pg_init_privs WHERE objoid=$1::oid AND classoid='pg_proc'::regclass AND objsubid=0 AND privtype='i'", f.oid).Scan(&initial); err != nil {
				t.Fatal(err)
			}
			if live != "{"+bootstrap+"=X/"+bootstrap+"}" || initial != live {
				t.Fatal("bootstrap ACL", live, initial)
			}
			query := "SELECT pg_catalog." + f.name + "(" + arg + ")"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			deny := func(executor fixture.Executor, q string) {
				step := fixture.ErrorDiagnostic(q, activityDiagnostic("42501", "permission denied for function "+f.name, 0))
				step.SelectRows = true
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			}
			activityExec(t, p, c, "SET ROLE database_reset_reader")
			deny(c, query)
			deny(held, query)
			if f.strict {
				resetDatabaseValue(t, p, b, c, "SELECT "+f.name+"(NULL::oid)", f.name, nil)
				deny(c, "SELECT "+f.name+"(v) FROM reset_runtime_null")
			}
			activityExec(t, p, c, "RESET ROLE")
			for _, grantee := range []string{"database_reset_reader", "database_reset_group", "PUBLIC"} {
				activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO "+grantee)
				if grantee == "database_reset_group" {
					activityExec(t, p, c, "GRANT database_reset_group TO database_reset_reader")
				}
				activityExec(t, p, c, "SET ROLE database_reset_reader")
				resetDatabaseValue(t, p, b, held, query, f.name, "")
				if f.strict {
					resetDatabaseValue(t, p, b, c, "SELECT "+f.name+"(v) FROM reset_runtime_null", f.name, nil)
				}
				activityExec(t, p, c, "RESET ROLE")
				activityExec(t, p, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+grantee)
				if grantee == "database_reset_group" {
					activityExec(t, p, c, "REVOKE database_reset_group FROM database_reset_reader")
				}
				activityExec(t, p, c, "SET ROLE database_reset_reader")
				deny(held, query)
				activityExec(t, p, c, "RESET ROLE")
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
