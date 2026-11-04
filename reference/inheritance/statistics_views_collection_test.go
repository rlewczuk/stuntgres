package reference_test

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsViewsCollection(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		statisticsViewsPopulation(s)
		s.exec("CREATE STATISTICS sv_d(ndistinct) ON a,b FROM expr_t", "CREATE STATISTICS sv_f(dependencies) ON a,b FROM expr_t", "CREATE STATISTICS sv_m(mcv) ON a,b FROM expr_t", "ANALYZE expr_t")
		s.run(statisticsViewsSkew("pg_stats", "tablename='expr_t' AND attname='a' AND NOT inherited", "{1,2}"))
		for _, name := range []string{"sv_expr", "sv_joint"} {
			s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='"+name+"' AND NOT inherited", "{2,3}"))
		}
		statisticsViewsSkewMCV(t, s)
		s.run(fixture.Select("SELECT statistics_name,attnames,exprs,kinds,inherited,n_distinct::text AS nd,dependencies::text AS deps,most_common_vals IS NULL AS no_mcv FROM pg_stats_ext WHERE statistics_name IN ('sv_expr','sv_joint','sv_d','sv_f') ORDER BY statistics_name",
			[]string{"statistics_name", "attnames", "exprs", "kinds", "inherited", "nd", "deps", "no_mcv"}, []string{"NAME", "_NAME", "_TEXT", "_CHAR", "BOOL", "TEXT", "TEXT", "BOOL"},
			[][]any{{"sv_d", "{a,b}", nil, "{d}", false, `{"1, 2": 3}`, nil, true}, {"sv_expr", nil, `{"(a + 1)"}`, "{e}", false, nil, nil, true}, {"sv_f", "{a,b}", nil, "{f}", false, nil, `{"1 => 2": 1.000000, "2 => 1": 1.000000}`, true}, {"sv_joint", "{b}", `{"(a + 1)"}`, "{d,f,m,e}", false, `{"2, -1": 3}`, `{"2 => -1": 1.000000, "-1 => 2": 1.000000}`, false}}))
		s.run(fixture.Select("SELECT n_distinct IS NULL AS no_nd,dependencies IS NULL AS no_deps,exprs IS NULL AS no_expr FROM pg_stats_ext WHERE statistics_name='sv_m'", []string{"no_nd", "no_deps", "no_expr"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		rows, closeRows := s.query(t, "SELECT most_common_vals,most_common_freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
		defer closeRows()
		statisticsViewsReadHeldSkew(t, s, rows)

		s.exec("CREATE TABLE sv_null(a INT)")
		s.run(fixture.Exec("INSERT INTO sv_null VALUES(NULL),(NULL),(NULL),(NULL),(NULL)", 5))
		s.exec("CREATE STATISTICS sv_null_s ON (a+1) FROM sv_null", "CREATE STATISTICS sv_coalesce ON (coalesce(a,0)) FROM sv_null", "ANALYZE sv_null")
		s.run(fixture.Select("SELECT expr,inherited,null_frac,avg_width,n_distinct,most_common_vals,most_common_freqs,histogram_bounds,correlation,most_common_elems,most_common_elem_freqs,elem_count_histogram FROM pg_stats_ext_exprs WHERE statistics_name='sv_null_s'",
			[]string{"expr", "inherited", "null_frac", "avg_width", "n_distinct", "most_common_vals", "most_common_freqs", "histogram_bounds", "correlation", "most_common_elems", "most_common_elem_freqs", "elem_count_histogram"}, []string{"TEXT", "BOOL", "FLOAT4", "INT4", "FLOAT4", "ANYARRAY", "_FLOAT4", "ANYARRAY", "FLOAT4", "ANYARRAY", "_FLOAT4", "_FLOAT4"},
			[][]any{{"(a + 1)", false, float64(1), int64(4), float64(0), nil, nil, nil, nil, nil, nil, nil}}))
		s.run(fixture.Select("SELECT expr,null_frac,avg_width,abs(n_distinct+0.2)<0.000001 AS distinct_fraction,most_common_vals::text AS vals,most_common_freqs::text AS freqs,histogram_bounds FROM pg_stats_ext_exprs WHERE statistics_name='sv_coalesce'",
			[]string{"expr", "null_frac", "avg_width", "distinct_fraction", "vals", "freqs", "histogram_bounds"}, []string{"TEXT", "FLOAT4", "INT4", "BOOL", "TEXT", "TEXT", "ANYARRAY"}, [][]any{{"COALESCE(a, 0)", float64(0), int64(4), true, "{0}", "{1}", nil}}))
	})
}

func TestReferenceStatisticsViewsHistogram(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		s.exec("CREATE TABLE sv_hist(a INT)")
		s.run(fixture.Exec("INSERT INTO sv_hist VALUES"+statisticsViewsSQLValues(200, "(%d)"), 200))
		s.exec("CREATE STATISTICS sv_hist_int ON (a+1) FROM sv_hist", "CREATE STATISTICS sv_hist_text ON (a::text) FROM sv_hist", "ANALYZE sv_hist")
		s.run(fixture.Select("SELECT null_frac,avg_width,n_distinct,most_common_vals,most_common_freqs,most_common_elems,most_common_elem_freqs,elem_count_histogram FROM pg_stats_ext_exprs WHERE statistics_name='sv_hist_int'",
			[]string{"null_frac", "avg_width", "n_distinct", "most_common_vals", "most_common_freqs", "most_common_elems", "most_common_elem_freqs", "elem_count_histogram"}, []string{"FLOAT4", "INT4", "FLOAT4", "ANYARRAY", "_FLOAT4", "ANYARRAY", "_FLOAT4", "_FLOAT4"}, [][]any{{float64(0), int64(4), float64(-1), nil, nil, nil, nil, nil}}))
		for _, name := range []string{"sv_hist_int", "sv_hist_text"} {
			for _, empty := range []bool{false, true} {
				query := "SELECT histogram_bounds FROM pg_stats_ext_exprs WHERE statistics_name=$1"
				if empty {
					query += " AND FALSE"
				}
				rows, closeRows := s.query(t, query, name)
				defer closeRows()
				statisticsViewsMetadata(t, s.pg, rows, []string{"histogram_bounds"}, []string{"ANYARRAY"})
				if empty {
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty histogram rows", rows.Err())
					}
					closeRows()
					continue
				}
				if !rows.Next() {
					t.Fatal("missing histogram", rows.Err())
				}
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				text := statisticsViewsRawText(t, s.pg, raw)
				if name == "sv_hist_int" {
					statisticsViewsIntegerHistogram(t, text)
				} else {
					array := statisticsViewsReadArray(t, text)
					if !reflect.DeepEqual(array.shape, []int{101}) || array.values[0] == nil || *array.values[0] != "1" || array.values[100] == nil || *array.values[100] != "99" {
						t.Fatalf("TEXT histogram endpoints/shape %q", text)
					}
					last := ""
					for _, bound := range array.values {
						if bound == nil || *bound <= last {
							t.Fatalf("TEXT histogram order %q", text)
						}
						last = *bound
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("histogram cardinality", rows.Err())
				}
				closeRows()
			}
		}
		// PostgreSQL measures physical correlation; native has no producer for
		// it. Do not fabricate that statistic just to make the views agree.
		var correlation any
		if s.pg {
			correlation = float64(1)
		}
		s.run(fixture.Select("SELECT correlation FROM pg_stats_ext_exprs WHERE statistics_name='sv_hist_int'", []string{"correlation"}, []string{"FLOAT4"}, [][]any{{correlation}}))
		statisticsViewsImportedSlots(t, s)
	})
}

func statisticsViewsImportedSlots(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	s.exec("CREATE TABLE ast_t(a INT,arr INT[],r int4range)")
	for _, tc := range []struct{ column, slots string }{
		{"a", "'most_common_vals','{1,2}','most_common_freqs','{0.5,0.25}'::real[],'histogram_bounds','[0:2]={3,5,9}','correlation',.75::real"},
		{"arr", "'most_common_elems','{1,2}','most_common_elem_freqs','{0.5,0.25,0.25,0.5,0}'::real[],'elem_count_histogram','{0,1,2,3}'::real[]"},
		{"r", `'range_length_histogram','{1,3,9}','range_empty_frac',.125::real,'range_bounds_histogram','{"[1,2)","[5,9)"}'`},
	} {
		s.run(fixture.Select(attributeStatsRestore(tc.column, false, tc.slots), []string{"pg_restore_attribute_stats"}, []string{"BOOL"}, [][]any{{true}}))
	}
	s.run(fixture.Select("SELECT most_common_vals::text AS vals,most_common_freqs::text AS freqs,histogram_bounds::text AS hist,correlation FROM pg_stats WHERE tablename='ast_t' AND attname='a'", []string{"vals", "freqs", "hist", "correlation"}, []string{"TEXT", "TEXT", "TEXT", "FLOAT4"}, [][]any{{"{1,2}", "{0.5,0.25}", "[0:2]={3,5,9}", float64(.75)}}))
	s.run(fixture.Select("SELECT most_common_elems::text AS elems,most_common_elem_freqs::text AS freqs,elem_count_histogram::text AS hist FROM pg_stats WHERE tablename='ast_t' AND attname='arr'", []string{"elems", "freqs", "hist"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"{1,2}", "{0.5,0.25,0.25,0.5,0}", "{0,1,2,3}"}}))
	s.run(fixture.Select("SELECT range_length_histogram::text AS lengths,range_empty_frac,range_bounds_histogram::text AS bounds FROM pg_stats WHERE tablename='ast_t' AND attname='r'", []string{"lengths", "range_empty_frac", "bounds"}, []string{"TEXT", "FLOAT4", "TEXT"}, [][]any{{"{1,3,9}", float64(.125), `{"[1,2)","[5,9)"}`}}))
}

func TestReferenceStatisticsViewsMCV(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		s.exec("CREATE TABLE sv_four(a INT,b INT)", "CREATE STATISTICS sv_four_s(mcv) ON b,a FROM sv_four")
		s.run(fixture.Exec("INSERT INTO sv_four VALUES(1,1),(1,1),(2,2),(NULL,NULL)", 4))
		s.exec("ANALYZE sv_four")
		statisticsViewsMCV(t, s, "sv_four_s", statisticsViewsMCVItem{[]any{"1", "1"}, .5, .25}, statisticsViewsMCVItem{[]any{"2", "2"}, .25, .0625}, statisticsViewsMCVItem{[]any{nil, nil}, .25, .0625})
		s.exec("CREATE TABLE sv_escape(a INT,b TEXT)", "CREATE STATISTICS sv_escape_s(mcv) ON b,(a+1),a FROM sv_escape")
		texts := []any{nil, "NULL", "", "comma,value", `quote"value`, `back\slash`}
		values := make([]string, 0, len(texts)*4)
		want := make([]statisticsViewsMCVItem, len(texts))
		for i, text := range texts {
			literal := "NULL"
			if text != nil {
				literal = "'" + strings.ReplaceAll(text.(string), "'", "''") + "'"
			}
			for range 4 {
				values = append(values, fmt.Sprintf("(%d,%s)", i+1, literal))
			}
			want[i] = statisticsViewsMCVItem{[]any{strconv.Itoa(i + 1), text, strconv.Itoa(i + 2)}, 1.0 / 6, 1.0 / 216}
		}
		s.run(fixture.Exec("INSERT INTO sv_escape VALUES"+strings.Join(values, ","), 24))
		s.exec("ANALYZE sv_escape")
		s.run(fixture.Select("SELECT attnames,exprs,kinds FROM pg_stats_ext WHERE statistics_name='sv_escape_s'", []string{"attnames", "exprs", "kinds"}, []string{"_NAME", "_TEXT", "_CHAR"}, [][]any{{"{a,b}", `{"(a + 1)"}`, "{m,e}"}}))
		statisticsViewsMCV(t, s, "sv_escape_s", want...)

		s.exec("CREATE TABLE sv_cap(a INT,b INT)", "CREATE STATISTICS sv_cap_s(mcv) ON a,b FROM sv_cap")
		s.run(fixture.Exec("INSERT INTO sv_cap VALUES(1,1),(1,1),(1,1),(1,1),(1,2),(1,2),(1,2),(2,2),(2,2),(NULL,NULL)", 10))
		s.exec("ALTER STATISTICS sv_cap_s SET STATISTICS 2", "ANALYZE sv_cap")
		statisticsViewsMCV(t, s, "sv_cap_s", statisticsViewsMCVItem{[]any{"1", "1"}, .4, .28}, statisticsViewsMCVItem{[]any{"1", "2"}, .3, .35})
		s.exec("ALTER STATISTICS sv_cap_s SET STATISTICS 1")
		// Target metadata does not retroactively truncate a captured list.
		statisticsViewsMCV(t, s, "sv_cap_s", statisticsViewsMCVItem{[]any{"1", "1"}, .4, .28}, statisticsViewsMCVItem{[]any{"1", "2"}, .3, .35})
		s.exec("ANALYZE sv_cap")
		statisticsViewsMCV(t, s, "sv_cap_s", statisticsViewsMCVItem{[]any{"1", "1"}, .4, .28})
	})
}
