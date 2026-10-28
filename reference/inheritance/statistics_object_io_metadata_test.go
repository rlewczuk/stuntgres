package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectIORoutineMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	want := [][]any{
		{int64(3355), "pg_ndistinct_in", "2275", int64(3361), "i", "s", true, "internal", "pg_ndistinct_in", float64(1)},
		{int64(3356), "pg_ndistinct_out", "3361", int64(2275), "i", "s", true, "internal", "pg_ndistinct_out", float64(1)},
		{int64(3357), "pg_ndistinct_recv", "2281", int64(3361), "s", "s", true, "internal", "pg_ndistinct_recv", float64(1)},
		{int64(3358), "pg_ndistinct_send", "3361", int64(17), "s", "s", true, "internal", "pg_ndistinct_send", float64(1)},
		{int64(3404), "pg_dependencies_in", "2275", int64(3402), "i", "s", true, "internal", "pg_dependencies_in", float64(1)},
		{int64(3405), "pg_dependencies_out", "3402", int64(2275), "i", "s", true, "internal", "pg_dependencies_out", float64(1)},
		{int64(3406), "pg_dependencies_recv", "2281", int64(3402), "s", "s", true, "internal", "pg_dependencies_recv", float64(1)},
		{int64(3407), "pg_dependencies_send", "3402", int64(17), "s", "s", true, "internal", "pg_dependencies_send", float64(1)},
		{int64(5018), "pg_mcv_list_in", "2275", int64(5017), "i", "s", true, "internal", "pg_mcv_list_in", float64(1)},
		{int64(5019), "pg_mcv_list_out", "5017", int64(2275), "i", "s", true, "internal", "pg_mcv_list_out", float64(1)},
		{int64(5020), "pg_mcv_list_recv", "2281", int64(5017), "s", "s", true, "internal", "pg_mcv_list_recv", float64(1)},
		{int64(5021), "pg_mcv_list_send", "5017", int64(17), "s", "s", true, "internal", "pg_mcv_list_send", float64(1)},
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(
			"SELECT p.oid::bigint AS oid,p.proname,p.proargtypes,p.prorettype::bigint AS prorettype,p.provolatile,p.proparallel,p.proisstrict,l.lanname,p.prosrc,p.procost FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE p.oid IN (3355,3356,3357,3358,3404,3405,3406,3407,5018,5019,5020,5021) ORDER BY p.oid",
			[]string{"oid", "proname", "proargtypes", "prorettype", "provolatile", "proparallel", "proisstrict", "lanname", "prosrc", "procost"},
			[]string{"INT8", "NAME", "OIDVECTOR", "INT8", "CHAR", "CHAR", "BOOL", "NAME", "TEXT", "FLOAT4"}, want,
		)
		if prepared {
			s = fixture.Prepared(s)
		}
		p.Step(t, s)
	}
}
