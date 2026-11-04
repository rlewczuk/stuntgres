package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type statisticsViewsShape struct {
	name         string
	names, types []string
	oids         []int64
}

func statisticsViewsShapes(pg bool) []statisticsViewsShape {
	return []statisticsViewsShape{
		{"pg_stats", []string{"schemaname", "tablename", "attname", "inherited", "null_frac", "avg_width", "n_distinct", "most_common_vals", "most_common_freqs", "histogram_bounds", "correlation", "most_common_elems", "most_common_elem_freqs", "elem_count_histogram", "range_length_histogram", "range_empty_frac", "range_bounds_histogram"},
			[]string{"NAME", "NAME", "NAME", "BOOL", "FLOAT4", "INT4", "FLOAT4", "ANYARRAY", "_FLOAT4", "ANYARRAY", "FLOAT4", "ANYARRAY", "_FLOAT4", "_FLOAT4", "ANYARRAY", "FLOAT4", "ANYARRAY"},
			[]int64{19, 19, 19, 16, 700, 23, 700, 2277, 1021, 2277, 700, 2277, 1021, 1021, 2277, 700, 2277}},
		{"pg_stats_ext", []string{"schemaname", "tablename", "statistics_schemaname", "statistics_name", "statistics_owner", "attnames", "exprs", "kinds", "inherited", "n_distinct", "dependencies", "most_common_vals", "most_common_val_nulls", "most_common_freqs", "most_common_base_freqs"},
			[]string{"NAME", "NAME", "NAME", "NAME", "NAME", "_NAME", "_TEXT", "_CHAR", "BOOL", statisticsDatabaseTypeName(pg, "pg_ndistinct"), statisticsDatabaseTypeName(pg, "pg_dependencies"), "_TEXT", "_BOOL", "_FLOAT8", "_FLOAT8"},
			[]int64{19, 19, 19, 19, 19, 1003, 1009, 1002, 16, 3361, 3402, 1009, 1000, 1022, 1022}},
		{"pg_stats_ext_exprs", []string{"schemaname", "tablename", "statistics_schemaname", "statistics_name", "statistics_owner", "expr", "inherited", "null_frac", "avg_width", "n_distinct", "most_common_vals", "most_common_freqs", "histogram_bounds", "correlation", "most_common_elems", "most_common_elem_freqs", "elem_count_histogram"},
			[]string{"NAME", "NAME", "NAME", "NAME", "NAME", "TEXT", "BOOL", "FLOAT4", "INT4", "FLOAT4", "ANYARRAY", "_FLOAT4", "ANYARRAY", "FLOAT4", "ANYARRAY", "_FLOAT4", "_FLOAT4"},
			[]int64{19, 19, 19, 19, 19, 25, 16, 700, 23, 700, 2277, 1021, 2277, 700, 2277, 1021, 1021}},
	}
}

func TestReferenceStatisticsViewsShape(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		for _, shape := range statisticsViewsShapes(s.pg) {
			t.Run(shape.name, func(t *testing.T) {
				run := func(step fixture.Step) {
					step.Prepared = s.prepared
					fixture.RunStep(t, s.pair.Ctx, s.conn, s.backend.Recorder, step)
				}
				for _, source := range []string{shape.name, "pg_catalog." + shape.name} {
					rows, closeRows := s.query(t, "SELECT * FROM "+source+" WHERE FALSE")
					defer closeRows()
					statisticsViewsMetadata(t, s.pg, rows, shape.names, shape.types)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("WHERE FALSE rows", rows.Err())
					}
					closeRows()
				}
				attributes := make([][]any, len(shape.names))
				for i, name := range shape.names {
					collation, dimensions := int64(0), int64(0)
					switch shape.oids[i] {
					case 19, 3361, 3402:
						collation = 950
					case 25:
						collation = 100
					case 1003:
						collation = 950
					case 1009:
						collation = 100
					}
					attributes[i] = []any{int64(i + 1), name, shape.oids[i], false, int64(-1), dimensions, collation}
				}
				run(fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,attnotnull,atttypmod::int4,attndims::int4,attcollation::bigint FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum",
					[]string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation"}, []string{"INT4", "TEXT", "INT8", "BOOL", "INT4", "INT4", "INT8"}, attributes, "pg_catalog."+shape.name))
				run(fixture.Select("SELECT c.relkind::text,c.relnamespace::bigint,c.relnatts::int4,c.relhasindex,c.reloptions::text,t.typname::text,t.typtype::text,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,a.typname::text AS array_name,a.typnamespace=c.relnamespace AS array_namespace FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=$1::regclass",
					[]string{"relkind", "relnamespace", "relnatts", "relhasindex", "reloptions", "typname", "typtype", "row_link", "element_link", "array_link", "array_name", "array_namespace"}, []string{"TEXT", "INT8", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "TEXT", "BOOL"},
					[][]any{{"v", int64(11), int64(len(shape.names)), false, "{security_barrier=true}", shape.name, "c", true, true, true, "_" + shape.name, true}}, "pg_catalog."+shape.name))
				owner := "postgres"
				if s.pg {
					owner = "stuntgres"
				}
				run(fixture.Select("SELECT relowner::regrole::text AS owner,relacl::text AS acl FROM pg_class WHERE oid=$1::regclass", []string{"owner", "acl"}, []string{"TEXT", "TEXT"}, [][]any{{owner, fmt.Sprintf("{%s=arwdDxtm/%s,=r/%s}", owner, owner, owner)}}, "pg_catalog."+shape.name))
			})
		}
		statisticsViewsPopulation(s)
		// NULL-only expression rows have exactly the same descriptor as analyzed
		// rows. No field's database type is inferred from its first datum.
		for _, analyzed := range []bool{false, true} {
			if analyzed {
				s.exec("ANALYZE expr_t")
			}
			for _, shape := range statisticsViewsShapes(s.pg) {
				for _, source := range []string{shape.name, "pg_catalog." + shape.name} {
					rows, closeRows := s.query(t, "SELECT * FROM "+source+" WHERE tablename='expr_t'")
					defer closeRows()
					statisticsViewsMetadata(t, s.pg, rows, shape.names, shape.types)
					count := 0
					for rows.Next() {
						count++
					}
					if rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					closeRows()
					want := 0
					if shape.name == "pg_stats_ext_exprs" {
						want = 2
					}
					if analyzed {
						want = 2
						if shape.name == "pg_stats" {
							want = 3
						}
					}
					if count != want {
						t.Fatalf("%s analyzed=%t rows%d want%d", source, analyzed, count, want)
					}
				}
			}
		}
	})
}

func TestReferenceStatisticsViewsMissingAnalysis(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		s.exec("CREATE TABLE sv_empty(a INT,b INT)", "CREATE STATISTICS sv_columns(ndistinct) ON b,a FROM sv_empty", "CREATE STATISTICS sv_single ON (a+1) FROM sv_empty", "CREATE STATISTICS sv_pair ON (b+2),(a+1) FROM sv_empty")
		want := [][]any{statisticsViewsUnbuilt("sv_pair", "(a + 1)"), statisticsViewsUnbuilt("sv_pair", "(b + 2)"), statisticsViewsUnbuilt("sv_single", "(a + 1)")}
		statisticsViewsMissing(s, "sv_empty", want)
		heldExt := statisticsViewsHeld(t, s, "SELECT count(*) FROM pg_stats_ext WHERE tablename='sv_empty'")
		defer heldExt.Close()
		heldExpr := statisticsViewsHeld(t, s, "SELECT count(*) FROM pg_stats_ext_exprs WHERE tablename='sv_empty' AND inherited IS NULL")
		defer heldExpr.Close()
		statisticsViewsHeldCount(t, s, heldExt, 0)
		statisticsViewsHeldCount(t, s, heldExpr, 3)
		s.exec("ANALYZE sv_empty")
		statisticsViewsMissing(s, "sv_empty", want)
		s.run(fixture.Exec("INSERT INTO sv_empty VALUES(1,10),(1,10),(1,10),(1,10)", 4))
		s.exec("ANALYZE sv_empty")
		statisticsViewsHeldCount(t, s, heldExt, 3)
		statisticsViewsHeldCount(t, s, heldExpr, 0)
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_columns'", 0))
		s.run(fixture.Select("SELECT attnames,exprs,kinds,inherited,n_distinct,dependencies,most_common_vals,most_common_val_nulls,most_common_freqs,most_common_base_freqs FROM pg_stats_ext WHERE statistics_name='sv_single'",
			[]string{"attnames", "exprs", "kinds", "inherited", "n_distinct", "dependencies", "most_common_vals", "most_common_val_nulls", "most_common_freqs", "most_common_base_freqs"}, []string{"_NAME", "_TEXT", "_CHAR", "BOOL", statisticsDatabaseTypeName(s.pg, "pg_ndistinct"), statisticsDatabaseTypeName(s.pg, "pg_dependencies"), "_TEXT", "_BOOL", "_FLOAT8", "_FLOAT8"},
			[][]any{{nil, `{"(a + 1)"}`, "{e}", false, nil, nil, nil, nil, nil, nil}}))
	})
}

func TestReferenceStatisticsViewsDiagnostics(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		for _, view := range []string{"pg_stats", "pg_stats_ext", "pg_stats_ext_exprs"} {
			for _, source := range []string{view, "pg_catalog." + view} {
				for _, column := range []string{"missing", "tableid", "statistics_id"} {
					s.run(statisticsViewsFailure("SELECT "+column+" FROM "+source, "42703", fmt.Sprintf("column %q does not exist", column), s.prepared))
				}
			}
			// A failure outside a transaction must not poison a real retained
			// prepared statement or publish an altered catalog descriptor.
			held := statisticsViewsHeld(t, s, "SELECT count(*) FROM pg_catalog."+view+" WHERE FALSE")
			defer held.Close()
			statisticsViewsHeldCount(t, s, held, 0)
			s.run(statisticsViewsFailure("SELECT missing FROM "+view, "42703", `column "missing" does not exist`, s.prepared))
			statisticsViewsHeldCount(t, s, held, 0)
		}
	})
}
