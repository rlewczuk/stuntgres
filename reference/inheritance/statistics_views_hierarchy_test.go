package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func statisticsViewsHierarchyExpressions(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT statistics_name,inherited,expr,null_frac,most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats_ext_exprs WHERE statistics_name IN ('sv_parent_s','sv_child_s','sv_part_s') ORDER BY statistics_name,inherited",
		[]string{"statistics_name", "inherited", "expr", "null_frac", "vals", "freqs"}, []string{"NAME", "BOOL", "TEXT", "FLOAT4", "TEXT", "TEXT"}, rows)
}

func TestReferenceStatisticsViewsHierarchy(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		s.exec("CREATE TABLE sv_parent(a INT,b INT)", "CREATE TABLE sv_child(extra INT,a INT,b INT)", "ALTER TABLE sv_child INHERIT sv_parent", "CREATE STATISTICS sv_parent_s(ndistinct) ON b,(a+1) FROM sv_parent", "CREATE STATISTICS sv_child_s(ndistinct) ON b,(a+1) FROM sv_child")
		s.run(fixture.Exec("INSERT INTO sv_parent VALUES(1,10),(1,10)", 2))
		s.run(fixture.Exec("INSERT INTO sv_child VALUES(777,2,20),(777,2,20),(777,2,20)", 3))
		s.run(fixture.Select("SELECT attnum,attname FROM pg_attribute WHERE attrelid='sv_child'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname"}, []string{"INT2", "NAME"}, [][]any{{int64(1), "extra"}, {int64(2), "a"}, {int64(3), "b"}}))
		s.run(statisticsViewsHierarchyExpressions([]any{"sv_child_s", nil, "(a + 1)", nil, nil, nil}, []any{"sv_parent_s", nil, "(a + 1)", nil, nil, nil}))
		s.exec("ANALYZE ONLY sv_parent")
		s.run(statisticsViewsHierarchyExpressions([]any{"sv_child_s", nil, "(a + 1)", nil, nil, nil}, []any{"sv_parent_s", false, "(a + 1)", float64(0), "{2}", "{1}"}, []any{"sv_parent_s", true, "(a + 1)", float64(0), "{3,2}", "{0.6,0.4}"}))
		s.run(fixture.Select("SELECT inherited,most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats WHERE tablename='sv_parent' AND attname='a' ORDER BY inherited", []string{"inherited", "vals", "freqs"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{false, "{1}", "{1}"}, {true, "{2,1}", "{0.6,0.4}"}}))
		s.run(fixture.Select("SELECT statistics_name,attnames,inherited,n_distinct::text AS nd FROM pg_stats_ext WHERE tablename IN ('sv_parent','sv_child') ORDER BY statistics_name,inherited", []string{"statistics_name", "attnames", "inherited", "nd"}, []string{"NAME", "_NAME", "BOOL", "TEXT"}, [][]any{{"sv_parent_s", "{b}", false, `{"2, -1": 1}`}, {"sv_parent_s", "{b}", true, `{"2, -1": 2}`}}))
		s.exec("ANALYZE sv_parent")
		s.run(statisticsViewsHierarchyExpressions([]any{"sv_child_s", false, "(a + 1)", float64(0), "{3}", "{1}"}, []any{"sv_parent_s", false, "(a + 1)", float64(0), "{2}", "{1}"}, []any{"sv_parent_s", true, "(a + 1)", float64(0), "{3,2}", "{0.6,0.4}"}))
		s.run(fixture.Select("SELECT n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_child_s'", []string{"nd"}, []string{"TEXT"}, [][]any{{`{"3, -1": 1}`}}))
		// Child-local facts are not recomputed by a parent-only refresh.
		s.run(fixture.Exec("UPDATE sv_child SET a=7,b=70", 3))
		s.exec("ANALYZE ONLY sv_parent")
		s.run(statisticsViewsHierarchyExpressions([]any{"sv_child_s", false, "(a + 1)", float64(0), "{3}", "{1}"}, []any{"sv_parent_s", false, "(a + 1)", float64(0), "{2}", "{1}"}, []any{"sv_parent_s", true, "(a + 1)", float64(0), "{8,2}", "{0.6,0.4}"}))

		s.exec("CREATE TABLE sv_part(a INT,b INT) PARTITION BY RANGE(a)", "CREATE TABLE sv_part_low PARTITION OF sv_part FOR VALUES FROM(0) TO(10)", "CREATE TABLE sv_part_high PARTITION OF sv_part FOR VALUES FROM(10) TO(20)", "CREATE STATISTICS sv_part_s(ndistinct) ON b,(a+1) FROM sv_part")
		s.run(fixture.Exec("INSERT INTO sv_part VALUES(1,10),(1,10),(1,10),(11,110),(11,110)", 5))
		s.exec("ANALYZE ONLY sv_part")
		s.run(fixture.Select("SELECT inherited,most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_part_s'", []string{"inherited", "vals", "freqs"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "{2,12}", "{0.6,0.4}"}}))
		s.run(fixture.Select("SELECT inherited,n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_part_s'", []string{"inherited", "nd"}, []string{"BOOL", "TEXT"}, [][]any{{true, `{"2, -1": 2}`}}))
		s.run(statisticsViewsCount("pg_stats", "tablename='sv_part' AND NOT inherited", 0))
		s.run(fixture.Select("SELECT count(*) FROM ONLY sv_parent", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		s.run(fixture.Select("SELECT count(*) FROM sv_parent", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))

		s.exec("ALTER TABLE sv_child NO INHERIT sv_parent", "ANALYZE sv_parent")
		parentRows := [][]any{{false, "{2}", "{1}"}}
		// PostgreSQL retains physical inherited statistics when ANALYZE finds
		// no remaining children. Native's generation/hierarchy compatibility
		// rejects the old inherited root rather than inventing a new one.
		if s.pg {
			parentRows = append(parentRows, []any{true, "{8,2}", "{0.6,0.4}"})
		}
		s.run(fixture.Select("SELECT inherited,most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_parent_s' ORDER BY inherited", []string{"inherited", "vals", "freqs"}, []string{"BOOL", "TEXT", "TEXT"}, parentRows))
		s.run(fixture.Select("SELECT inherited,most_common_vals::text AS vals FROM pg_stats_ext_exprs WHERE statistics_name='sv_child_s'", []string{"inherited", "vals"}, []string{"BOOL", "TEXT"}, [][]any{{false, "{3}"}}))
		s.exec("DROP TABLE sv_child")
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_child_s'", 0))
		s.run(statisticsViewsCount("pg_stats_ext", "statistics_name='sv_child_s'", 0))
	})
}
