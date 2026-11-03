package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pvc_smoke_reader"})
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvcChecks(t, p, b, c, nil, nil)
		pvcTruncate(t, p, b, c, nil)
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		pvOneFrozen(t, p, b, c)
		q := "SELECT * FROM pv_part.pg_visibility($1::regclass,0)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		pvcTruncate(t, p, b, c, "pv_part.heap")
		for range 2 {
			pvcSuppressed(t, p, b, c)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{false, false, true}}, "pv_part.heap"))
		}
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
		pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		defer pvRollback(c)
		pvcTruncate(t, p, b, c, "pv_part.heap")
		pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		pvcSuppressed(t, p, b, c)
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
		for _, q := range []string{"CREATE ROLE pvc_smoke_reader", "GRANT USAGE ON SCHEMA pv_part TO pvc_smoke_reader", "GRANT pg_stat_scan_tables TO pvc_smoke_reader", "SET SESSION AUTHORIZATION pvc_smoke_reader"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvBoth(t, p, b, c, pvError("SELECT pv_part.pg_truncate_visibility_map('pv_part.heap')", "42501", "permission denied for function pg_truncate_visibility_map"))
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION pv_part.pg_truncate_visibility_map(regclass) TO pvc_smoke_reader", "SET SESSION AUTHORIZATION pvc_smoke_reader"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvBoth(t, p, b, c, pvError("SELECT k FROM pv_part.heap", "42501", "permission denied for table heap"))
		pvcTruncate(t, p, b, c, "pv_part.heap")
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		pvcSuppressed(t, p, b, c)
		for _, q := range []string{"CREATE VIEW pv_part.smoke_check AS SELECT * FROM pv_part.pg_check_visible('pv_part.heap'::regclass)", "CREATE VIEW pv_part.smoke_mutator AS SELECT pv_part.pg_truncate_visibility_map('pv_part.heap'::regclass) IS NULL AS was_null", "VACUUM pv_part.heap"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvOneFrozen(t, p, b, c)
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			pvOneFrozen(t, p, b, c)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_visibility' AND d.classid='pg_proc'::regclass AND d.deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{true, true, true}}, "pv_part.heap"))
		}
		pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.smoke_check ORDER BY t_ctid", []string{"t_ctid"}, []string{"TID"}, nil))
		pvBoth(t, p, b, c, fixture.Select("SELECT was_null FROM pv_part.smoke_mutator", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
		pvcSuppressed(t, p, b, c)
	})
}
