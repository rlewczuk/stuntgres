package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGINMaintenanceBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "gin_maint_ns")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		defer conn.ExecContext(p.Ctx, "RESET search_path")
		ginMaintSeed(run, false)
		run(ginMaintPositive(ginMaintPositiveSQL))
		run(ginMaintResult(ginMaintCleanSQL, int64(0)))
		var oid int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'gin_maint_oracle_i'::regclass::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		for i, conversion := range []struct {
			typ   string
			value any
		}{{"text", "public.gin_maint_oracle_i"}, {"varchar", "public.gin_maint_oracle_i"}, {"integer", oid}, {"bigint", oid}, {"oid", oid}, {"regclass", oid}} {
			run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES("+strconv.Itoa(i+2)+",'{2}')", 1))
			q := "SELECT GIN_CLEAN_PENDING_LIST($1::" + conversion.typ + ")"
			run(ginMaintPositive(q+" > 0 AS cleaned", conversion.value))
			classRoutes(t, p, b, conn, ginMaintResult(q, int64(0), conversion.value))
			fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, ginMaintResult(q, int64(0), conversion.value))
		}
		q := "SELECT pg_catalog.gin_clean_pending_list($1::regclass)"
		raw, positive := classHeld(t, p, conn, q), classHeld(t, p, conn, q+" > 0 AS cleaned")
		run(fixture.Exec("ALTER INDEX gin_maint_oracle_i RENAME TO gin_maint_renamed", 0))
		run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(8,'{2}')", 1))
		fixture.RunStep(t, p.Ctx, positive, b.Recorder, ginMaintPositive(q+" > 0 AS cleaned", oid))
		fixture.RunStep(t, p.Ctx, raw, b.Recorder, ginMaintResult(q, int64(0), oid))
		run(fixture.Exec("CREATE SCHEMA gin_maint_ns", 0))
		run(fixture.Exec("CREATE FUNCTION gin_maint_ns.gin_clean_pending_list(regclass) RETURNS bigint IMMUTABLE RETURN -7", 0))
		run(fixture.Exec("SET search_path=gin_maint_ns,pg_catalog,public", 0))
		run(fixture.Exec("INSERT INTO public.gin_maint_oracle VALUES(9,'{2}')", 1))
		shadow := "SELECT gin_clean_pending_list('public.gin_maint_renamed'::regclass)"
		classRoutes(t, p, b, conn, ginMaintResult(shadow, int64(-7)))
		fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, shadow), b.Recorder, ginMaintResult(shadow, int64(-7)))
		run(ginMaintPositive("SELECT pg_catalog.gin_clean_pending_list('public.gin_maint_renamed'::regclass)>0 AS cleaned"))
		run(ginMaintResult("SELECT pg_catalog.gin_clean_pending_list('public.gin_maint_renamed'::regclass)", int64(0)))
		run(fixture.Exec("RESET search_path", 0))
		run(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}))
	})
}
