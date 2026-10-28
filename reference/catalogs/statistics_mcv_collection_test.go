package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func mcvSetup(t *testing.T, s ndistinctScenario) {
	t.Helper()
	s.run(fixture.Exec("CREATE TABLE mcv_t(a INT,b INT)", 0))
	s.run(fixture.Exec("CREATE STATISTICS mcv_s(mcv) ON a,b FROM mcv_t", 0))
	s.run(fixture.Select("SELECT count(*) FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_s'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	s.run(fixture.Select("SELECT (SELECT d.stxdmcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_s') AS v", []string{"v"}, []string{statisticsDatabaseTypeName(s.pg, "pg_mcv_list")}, [][]any{{nil}}))
	s.run(fixture.Exec("ANALYZE mcv_t", 0))
	s.run(fixture.Select("SELECT count(*) FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_s'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	s.run(fixture.Exec("INSERT INTO mcv_t VALUES(1,1),(1,1),(2,2),(NULL,NULL)", 4))
	s.run(fixture.Exec("ANALYZE mcv_t", 0))
	mcvAssert(t, s, "mcv_s", mcvFour...)
}
func mcvOrdered(s ndistinctScenario, five bool) {
	rows := [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}}
	if five {
		rows = append(rows, []any{int64(2), int64(2)})
	}
	rows = append(rows, []any{nil, nil})
	s.run(fixture.Select("SELECT a,b FROM mcv_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, rows))
}
func TestReferenceStatisticsMCVCollection(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		id := statisticExtDataID(t, s.conn, "mcv_s", "mcv_t")
		mcvOrdered(s, false)
		exprType := "_PG_STATISTIC"
		if s.pg {
			exprType = ""
		}
		s.run(fixture.Select("SELECT stxdndistinct,stxddependencies,stxdexpr FROM pg_statistic_ext_data", []string{"stxdndistinct", "stxddependencies", "stxdexpr"}, []string{statisticsDatabaseTypeName(s.pg, "pg_ndistinct"), statisticsDatabaseTypeName(s.pg, "pg_dependencies"), exprType}, [][]any{{nil, nil, nil}}))
		for _, q := range []string{"CREATE STATISTICS mcv_default ON a,b FROM mcv_t", "CREATE STATISTICS mcv_mixed(ndistinct,dependencies,mcv) ON a,b FROM mcv_t", "ANALYZE mcv_t"} {
			s.run(fixture.Exec(q, 0))
		}
		mcvAssert(t, s, "mcv_default", mcvFour...)
		mcvAssert(t, s, "mcv_mixed", mcvFour...)
		if statisticExtDataID(t, s.conn, "mcv_s", "mcv_t") != id {
			t.Fatal("ANALYZE changed definition")
		}
		mcvOrdered(s, false)
		s.run(fixture.Exec("DELETE FROM mcv_t", 4))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvAssert(t, s, "mcv_s", mcvFour...)
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(NULL,NULL),(NULL,NULL)", 2))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvAssert(t, s, "mcv_s", mcvExpected{"NULL,NULL", 1, 1})
	})
}
func mcvRefresh(t *testing.T, s ndistinctScenario) {
	t.Helper()
	before, _ := readMCV(t, s, "mcv_s", false)
	s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
	stale, _ := readMCV(t, s, "mcv_s", false)
	mcvSame(t, before, stale)
	mcvOrdered(s, true)
	s.run(fixture.Exec("ANALYZE mcv_t", 0))
	mcvAssert(t, s, "mcv_s", mcvFive...)
	mcvOrdered(s, true)
}
func TestReferenceStatisticsMCVRefresh(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) { mcvSetup(t, s); mcvRefresh(t, s) })
}
func mcvTargets(t *testing.T, s ndistinctScenario) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE mcv_cap(a INT,b INT)", "CREATE STATISTICS mcv_cap_s(mcv) ON a,b FROM mcv_cap"} {
		s.run(fixture.Exec(q, 0))
	}
	s.run(fixture.Exec("INSERT INTO mcv_cap VALUES(1,1),(1,1),(1,1),(1,1),(1,2),(1,2),(1,2),(2,2),(2,2),(NULL,NULL)", 10))
	all := []mcvExpected{{"1,1", .4, .28}, {"1,2", .3, .35}, {"2,2", .2, .1}, {"NULL,NULL", .1, .01}}
	for _, target := range []string{"1", "2", "0", "-1", "DEFAULT", "2"} {
		s.run(fixture.Exec("SET default_statistics_target=1", 0))
		s.run(fixture.Exec("ALTER STATISTICS mcv_cap_s SET STATISTICS "+target, 0))
		s.run(fixture.Exec("ANALYZE mcv_cap", 0))
		n := 1
		if target == "2" || target == "0" {
			n = 2
		}
		mcvAssert(t, s, "mcv_cap_s", all[:n]...)
		s.run(fixture.Select("SELECT a,b FROM mcv_cap ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(2)}, {int64(1), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}))
	}
	s.run(fixture.Exec("ALTER STATISTICS mcv_cap_s SET STATISTICS 100", 0))
	s.run(fixture.Exec("ANALYZE mcv_cap", 0))
	mcvAssert(t, s, "mcv_cap_s", all...)
	s.run(fixture.Select("SELECT count(*) FROM mcv_cap", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}))
	s.run(fixture.Exec("RESET default_statistics_target", 0))
}
func TestReferenceStatisticsMCVTargets(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvTargets(t, s)
		mcvSetup(t, s)
		if !s.pg {
			before, _ := readMCV(t, s, "mcv_s", false)
			s.run(fixture.Exec("ANALYZE mcv_t", 0))
			after, _ := readMCV(t, s, "mcv_s", false)
			mcvSame(t, before, after)
		}
		before, _ := readMCV(t, s, "mcv_s", false)
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		s.run(fixture.Exec("ANALYZE mcv_t(a)", 0))
		after, _ := readMCV(t, s, "mcv_s", false)
		mcvSame(t, before, after)
	})
}
func TestReferenceStatisticsMCVFeatureSmoke(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		mcvRefresh(t, s)
		mcvTargets(t, s)
		mcvSmokeAuthority(t, s)
		if !s.pg {
			mcvStaleControl(t, s)
		}
	})
}
