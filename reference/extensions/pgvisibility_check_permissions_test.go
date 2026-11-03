package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pvc_owner", "pvc_reader", "pvc_default"})
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE pvc_owner", "CREATE ROLE pvc_reader", "GRANT USAGE ON SCHEMA pv_part TO pvc_owner,pvc_reader", "ALTER TABLE pv_part.heap OWNER TO pvc_owner", "CREATE TABLE pv_part.inputs(n text,z integer)", "GRANT SELECT ON pv_part.inputs TO pvc_owner,pvc_reader"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.inputs VALUES(NULL,0)", 1))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		held := make([]fixture.HeldStatement, 3)
		failing := make([]fixture.HeldStatement, 3)
		for i, fn := range pvcFunctions {
			for j, q := range []string{"SELECT s.* FROM pv_part.inputs i CROSS JOIN LATERAL pv_part." + fn + "(COALESCE($1::text,i.n)::regclass) s", "SELECT s.* FROM pv_part.inputs i CROSS JOIN LATERAL pv_part." + fn + "(CASE WHEN i.z=0 THEN (1/i.z)::oid::regclass ELSE NULL::regclass END) s"} {
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				h := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				if j == 0 {
					held[i] = h
				} else {
					failing[i] = h
				}
			}
		}
		denied := func(i int) {
			for _, arg := range []any{nil, "pv_part.heap"} {
				s := pvError(held[i].SQL, "42501", "permission denied for function "+pvcFunctions[i], arg)
				pvBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			s := pvError(failing[i].SQL, "42501", "permission denied for function "+pvcFunctions[i])
			pvBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, failing[i], b.Recorder, s)
		}
		allowed := func(i int) {
			for _, arg := range []any{nil, "pv_part.heap"} {
				name, typ := "t_ctid", "TID"
				var rows [][]any
				if i == 2 {
					name = "s"
					typ = "VOID"
					var value any = ""
					if arg == nil {
						value = nil
					}
					rows = [][]any{{value}}
				}
				s := fixture.Select(held[i].SQL, []string{name}, []string{typ}, rows, arg)
				pvBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			s := pvError(failing[i].SQL, "22012", "division by zero")
			pvBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, failing[i], b.Recorder, s)
		}
		pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pvc_owner", 0))
		for i := range pvcFunctions {
			denied(i)
		}
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			pvRun(t, p, b, c, fixture.Exec("GRANT "+role+" TO pvc_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pvc_reader", 0))
			allowed(0)
			allowed(1)
			denied(2)
			pvBoth(t, p, b, c, pvError("SELECT k FROM pv_part.heap", "42501", "permission denied for table heap"))
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pvRun(t, p, b, c, fixture.Exec("REVOKE "+role+" FROM pvc_reader", 0))
		}
		for i, fn := range pvcFunctions {
			pvRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION pv_part."+fn+"(regclass) TO pvc_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pvc_reader", 0))
			allowed(i)
			pvBoth(t, p, b, c, pvError("SELECT k FROM pv_part.heap", "42501", "permission denied for table heap"))
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pvRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION pv_part."+fn+"(regclass) FROM pvc_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pvc_reader", 0))
			denied(i)
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, q := range []string{"ALTER ROLE pvc_reader NOINHERIT", "GRANT pg_stat_scan_tables TO pvc_reader", "SET SESSION AUTHORIZATION pvc_reader"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for i := range pvcFunctions {
			denied(i)
		}
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON SCHEMA pv_part TO pg_stat_scan_tables", "GRANT SELECT ON pv_part.inputs TO pg_stat_scan_tables", "SET SESSION AUTHORIZATION pvc_reader", "SET ROLE pg_stat_scan_tables"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		allowed(0)
		allowed(1)
		denied(2)
		for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE USAGE ON SCHEMA pv_part FROM pg_stat_scan_tables", "REVOKE SELECT ON pv_part.inputs FROM pg_stat_scan_tables", "REVOKE pg_stat_scan_tables FROM pvc_reader", "REVOKE USAGE ON SCHEMA pv_part FROM pvc_reader", "SET SESSION AUTHORIZATION pvc_reader"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, fn := range pvcFunctions {
			s := pvError("SELECT * FROM pv_part."+fn+"(NULL::regclass)", "42501", "permission denied for schema pv_part")
			s.Diagnostic.Position = "15"
			pvBoth(t, p, b, c, s)
		}
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{"CREATE ROLE pvc_default", "DROP EXTENSION pg_visibility", "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO pvc_default WITH GRANT OPTION", "CREATE EXTENSION pg_visibility SCHEMA pv_part"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM pvc_default")
		pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_init_privs i ON i.objoid=p.oid AND i.classoid='pg_proc'::regclass AND i.objsubid=0 AND i.privtype='e' WHERE n.nspname='pv_part' AND has_function_privilege('pvc_default',p.oid,'EXECUTE WITH GRANT OPTION') AND i.initprivs=p.proacl", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	})
}
