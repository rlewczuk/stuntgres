package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessSpecialSnapshots(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE public.special_snapshot_guard(x integer)", 0))
		run(fixture.Exec("INSERT INTO public.special_snapshot_guard VALUES(1)", 1))
		// A FROM clause and VOLATILE body prevent scalar SQL inlining. No
		// outer LO call freezes the frontier before the creator activation.
		run(fixture.Exec("CREATE FUNCTION public.special_snapshot_create(oid) RETURNS oid LANGUAGE SQL VOLATILE AS 'SELECT lo_create($1) FROM public.special_snapshot_guard'", 0))
		for i, tc := range []struct {
			priv string
			want any
		}{{"SELECT", nil}, {"UPDATE", true}, {"UPDATE WITH GRANT OPTION", nil}, {"SELECT,UPDATE", true}, {"SELECT,UPDATE WITH GRANT OPTION", nil}} {
			oid := 910202 + i
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(public.special_snapshot_create(%d::oid),'%s') AS v", oid, tc.priv), tc.want))
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(%d::oid,'SELECT') AS v", oid), true))
			run(largeObjectValue(fmt.Sprintf("lo_unlink(%d::oid)", oid), "INT4", int64(1)))
		}
		// Nested CASE makes creation precede a real current-authority consumer,
		// which must not advance the enclosing query's active LO snapshot.
		for i, tc := range []struct {
			priv string
			want any
		}{{"SELECT", nil}, {"UPDATE", true}, {"UPDATE WITH GRANT OPTION", nil}, {"SELECT,UPDATE", true}, {"SELECT,UPDATE WITH GRANT OPTION", nil}} {
			oid := 910202 + i
			target := fmt.Sprintf("CASE WHEN public.special_snapshot_create(%d::oid)=%d::oid THEN CASE WHEN has_parameter_privilege('lc_messages','SET') THEN %d::oid ELSE 0::oid END ELSE 0::oid END", oid, oid, oid)
			run(objectGrantAccessValue("SELECT has_largeobject_privilege("+target+",'"+tc.priv+"') AS v", tc.want))
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(%d::oid,'SELECT') AS v", oid), true))
			run(largeObjectValue(fmt.Sprintf("lo_unlink(%d::oid)", oid), "INT4", int64(1)))
		}
		for i, tc := range []struct {
			priv string
			want any
		}{{"SELECT", nil}, {"UPDATE", true}, {"UPDATE WITH GRANT OPTION", nil}, {"SELECT,UPDATE", true}, {"SELECT,UPDATE WITH GRANT OPTION", nil}} {
			oid := 910202 + i
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(lo_create(%d::oid),'%s') AS v", oid, tc.priv), tc.want))
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(%d::oid,'SELECT') AS v", oid), true))
		}
		run(fixture.Exec("CREATE FUNCTION public.special_snapshot_inquiry(oid) RETURNS boolean LANGUAGE SQL VOLATILE AS 'SELECT has_largeobject_privilege($1,''SELECT'') FROM public.special_snapshot_guard'", 0))
		run(fixture.Select("SELECT public.special_snapshot_inquiry(lo_create(910207::oid)) AS body_value,has_largeobject_privilege(910207::oid,'SELECT') AS outer_value", []string{"body_value", "outer_value"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}}))
		run(objectGrantAccessValue("SELECT has_largeobject_privilege(910207::oid,'SELECT') AS v", true))
		for _, tc := range []struct {
			oid  int
			priv string
			want any
		}{{910208, "SELECT", true}, {910209, "UPDATE", nil}} {
			run(largeObjectValue(fmt.Sprintf("lo_create(%d::oid)", tc.oid), "OID", int64(tc.oid)))
			target := fmt.Sprintf("CASE WHEN lo_unlink(%d::oid)=1 THEN %d::oid ELSE 0::oid END", tc.oid, tc.oid)
			run(objectGrantAccessValue("SELECT has_largeobject_privilege("+target+",'"+tc.priv+"') AS v", tc.want))
			run(objectGrantAccessValue(fmt.Sprintf("SELECT has_largeobject_privilege(%d::oid,'SELECT') AS v", tc.oid), nil))
		}
		// The outer direct read must not inherit the body's fresh snapshot. Its
		// failure rolls back even the argument-side creation from the same statement.
		run(largeObjectMissing("SELECT public.special_snapshot_inquiry(lo_create(910208::oid)),lo_get(910208::oid)", 910208))
		run(objectGrantAccessValue("SELECT has_largeobject_privilege(910208::oid,'SELECT') AS v", nil))
	})
}
