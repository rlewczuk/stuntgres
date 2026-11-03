package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetSharedPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"shared_reset_reader", "shared_reset_group"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bootstrap := "postgres"
		if b.PG {
			bootstrap = "stuntgres"
		}
		activityExec(t, p, c, "CREATE ROLE shared_reset_reader")
		activityExec(t, p, c, "CREATE ROLE shared_reset_group")
		activityExec(t, p, c, "GRANT pg_monitor,pg_read_all_stats TO shared_reset_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		for _, f := range resetSharedFunctions {
			identity := "pg_catalog." + f.name + "(" + f.argument + ")"
			defer func() {
				c.ExecContext(context.Background(), "RESET ROLE")
				c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM shared_reset_reader,shared_reset_group,PUBLIC")
			}()
			var live, initial string
			if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&live); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT initprivs::text FROM pg_init_privs WHERE objoid=$1::oid AND classoid='pg_proc'::regclass AND objsubid=0 AND privtype='i'", f.oid).Scan(&initial); err != nil {
				t.Fatal(err)
			}
			if live != "{"+bootstrap+"=X/"+bootstrap+"}" || initial != live {
				t.Fatal("restricted bootstrap ACL", live, initial)
			}
			query := "SELECT pg_catalog." + f.name + "($1::" + f.argument + ")"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			var argument any = "io"
			if f.name == "pg_stat_reset_slru" {
				argument = "transaction"
			}
			if f.strict {
				argument = int64(0)
			}
			deny := func() {
				step := fixture.ErrorDiagnostic(query, activityDiagnostic("42501", "permission denied for function "+f.name, 0), argument)
				step.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
			activityExec(t, p, c, "SET ROLE shared_reset_reader")
			deny()
			if f.strict {
				resetSharedNull(t, p, b, c, "SELECT pg_stat_reset_backend_stats(NULL::integer)")
			} else {
				resetReplicationRuntimeError(t, p, b, c, "SELECT "+f.name+"(NULL)", activityDiagnostic("42501", "permission denied for function "+f.name, 0))
			}
			activityExec(t, p, c, "RESET ROLE")
			for _, grantee := range []string{"shared_reset_reader", "shared_reset_group", "PUBLIC"} {
				activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO "+grantee)
				if grantee == "shared_reset_group" {
					activityExec(t, p, c, "GRANT shared_reset_group TO shared_reset_reader")
				}
				activityExec(t, p, c, "SET ROLE shared_reset_reader")
				resetReplicationVoid(t, p, b, held, query, f.name, argument)
				activityExec(t, p, c, "RESET ROLE")
				activityExec(t, p, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+grantee)
				if grantee == "shared_reset_group" {
					activityExec(t, p, c, "REVOKE shared_reset_group FROM shared_reset_reader")
				}
				activityExec(t, p, c, "SET ROLE shared_reset_reader")
				deny()
				activityExec(t, p, c, "RESET ROLE")
			}
		}
	})
}
