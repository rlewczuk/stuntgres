package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessSpecialAuthority(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		lo := accessExecutionHeld(t, p, c, "SELECT has_largeobject_privilege('og_object_reader',910201::oid,'SELECT') AS v")
		param := accessExecutionHeld(t, p, c, "SELECT has_parameter_privilege('og_object_reader','lc_messages','SET') AS v")
		check := func(want bool) {
			for _, h := range []fixture.HeldStatement{lo, param} {
				s := objectGrantAccessValue(h.SQL, want)
				run(s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
		}
		check(true)
		run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 910201 FROM og_object_reader", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_reader", 0))
		check(false)
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO og_object_reader", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
		check(true)
		run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT 910201 FROM og_object_reader", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_reader", 0))
		run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_other", 0))
		check(true)
		run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
		check(false)
		run(fixture.Exec("ALTER ROLE og_object_reader NOINHERIT", 0))
		run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
		check(false)
		run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
		run(fixture.Exec("ALTER ROLE og_object_reader INHERIT", 0))
		omitted := accessExecutionHeld(t, p, c, "SELECT has_parameter_privilege('lc_messages','SET') AS v")
		for _, tc := range []struct {
			actor string
			want  bool
		}{{"og_object_reader", false}, {"og_object_other", true}, {"og_object_reader", false}} {
			run(fixture.Exec("SET ROLE "+tc.actor, 0))
			fixture.RunStep(t, p.Ctx, omitted, b.Recorder, objectGrantAccessValue(omitted.SQL, tc.want))
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE VIEW public.special_invoker WITH (security_invoker=true) AS SELECT has_parameter_privilege('lc_messages','SET') AS v", 0))
		run(fixture.Exec("GRANT SELECT ON public.special_invoker TO og_object_reader,og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT v FROM public.special_invoker", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select("SELECT v FROM public.special_invoker", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		})
		calls := accessSpecialCalls(t, p, c)
		oldOID := accessExecutionHeld(t, p, c, calls[4].query)
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
		run(objectGrantAccessValue(param.SQL, true))
		fixture.RunStep(t, p.Ctx, param, b.Recorder, objectGrantAccessValue(param.SQL, true))
		run(objectGrantAccessValue(oldOID.SQL, true, calls[4].args...))
		fixture.RunStep(t, p.Ctx, oldOID, b.Recorder, objectGrantAccessValue(oldOID.SQL, true, calls[4].args...))
		// A NAME recipe follows recreation; the raw OID recipe keeps identity.
		run(fixture.Exec("DROP OWNED BY og_object_reader CASCADE", 0))
		run(fixture.Exec("DROP ROLE og_object_reader", 0))
		run(accessExecutionError(param.SQL, "42704", `role "og_object_reader" does not exist`))
		run(objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		fixture.RunStep(t, p.Ctx, param, b.Recorder, accessExecutionError(param.SQL, "42704", `role "og_object_reader" does not exist`))
		fixture.RunStep(t, p.Ctx, oldOID, b.Recorder, objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		run(fixture.Exec("CREATE ROLE og_object_reader", 0))
		var replacementOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_reader'").Scan(&replacementOID); err != nil {
			t.Fatal(err)
		}
		if replacementOID <= 0 || replacementOID == calls[4].args[0].(int64) {
			t.Fatalf("replacement role OID %d must be valid and differ from prior OID %v", replacementOID, calls[4].args[0])
		}
		run(objectGrantAccessValue(param.SQL, false))
		fixture.RunStep(t, p.Ctx, param, b.Recorder, objectGrantAccessValue(param.SQL, false))
		run(objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		fixture.RunStep(t, p.Ctx, oldOID, b.Recorder, objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
		run(objectGrantAccessValue(param.SQL, true))
		run(objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		fixture.RunStep(t, p.Ctx, param, b.Recorder, objectGrantAccessValue(param.SQL, true))
		fixture.RunStep(t, p.Ctx, oldOID, b.Recorder, objectGrantAccessValue(oldOID.SQL, false, calls[4].args...))
		run(largeObjectValue("lo_unlink(910201::oid)", "INT4", int64(1)))
		fixture.RunStep(t, p.Ctx, lo, b.Recorder, objectGrantAccessValue(lo.SQL, nil))
		run(largeObjectValue("lo_create(910201::oid)", "OID", int64(910201)))
		fixture.RunStep(t, p.Ctx, lo, b.Recorder, objectGrantAccessValue(lo.SQL, false))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO og_object_reader", 0))
		fixture.RunStep(t, p.Ctx, lo, b.Recorder, objectGrantAccessValue(lo.SQL, true))
	})
}
