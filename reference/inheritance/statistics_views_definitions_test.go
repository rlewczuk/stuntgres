package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsViewsDefinitions(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		s.exec("CREATE ROLE sv_def_owner", "CREATE SCHEMA sv_defs", "CREATE TABLE sv_def_t(spare INT,a INT,b INT)", "ALTER TABLE sv_def_t DROP COLUMN spare", "CREATE STATISTICS sv_def_columns(ndistinct) ON b,a FROM sv_def_t", "CREATE STATISTICS sv_def_expr ON (b+2),(a+1) FROM sv_def_t", "CREATE STATISTICS sv_def_mixed ON b,(a+1) FROM sv_def_t")
		s.run(fixture.Exec("INSERT INTO sv_def_t(a,b) VALUES(1,10),(1,10),(1,10),(1,10)", 4))
		s.exec("ANALYZE sv_def_t")
		id := statisticExtDataID(t, s.conn, "sv_def_mixed", "sv_def_t")
		s.run(fixture.Select("SELECT statistics_schemaname,statistics_name,attnames,exprs,kinds FROM pg_stats_ext WHERE tablename='sv_def_t' ORDER BY statistics_name", []string{"statistics_schemaname", "statistics_name", "attnames", "exprs", "kinds"}, []string{"NAME", "NAME", "_NAME", "_TEXT", "_CHAR"},
			[][]any{{"public", "sv_def_columns", "{a,b}", nil, "{d}"}, {"public", "sv_def_expr", nil, `{"(b + 2)","(a + 1)"}`, "{d,f,m,e}"}, {"public", "sv_def_mixed", "{b}", `{"(a + 1)"}`, "{d,f,m,e}"}}))
		s.run(fixture.Select("SELECT expr,most_common_vals::text AS vals FROM pg_stats_ext_exprs WHERE statistics_name='sv_def_expr' ORDER BY expr", []string{"expr", "vals"}, []string{"TEXT", "TEXT"}, [][]any{{"(a + 1)", "{2}"}, {"(b + 2)", "{12}"}}))
		s.run(fixture.Select("SELECT attnum,attname FROM pg_attribute WHERE attrelid='sv_def_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname"}, []string{"INT2", "NAME"}, [][]any{{int64(2), "a"}, {int64(3), "b"}}))
		s.run(fixture.Select("SELECT n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_def_columns'", []string{"nd"}, []string{"TEXT"}, [][]any{{`{"2, 3": 1}`}}))
		s.exec("ALTER TABLE sv_def_t RENAME COLUMN a TO renamed", "ALTER TABLE sv_def_t RENAME TO sv_def_renamed", "ALTER TABLE sv_def_renamed SET SCHEMA sv_defs", "ALTER STATISTICS sv_def_mixed RENAME TO sv_def_moved", "ALTER STATISTICS sv_def_moved SET SCHEMA sv_defs", "ALTER STATISTICS sv_defs.sv_def_moved OWNER TO sv_def_owner")
		s.run(fixture.Select("SELECT oid::bigint=$1 AS same_identity FROM pg_statistic_ext WHERE stxname='sv_def_moved'", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}, id))
		s.run(fixture.Select("SELECT schemaname,tablename,statistics_schemaname,statistics_name,statistics_owner,attnames,exprs,kinds,inherited,n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_def_moved'", []string{"schemaname", "tablename", "statistics_schemaname", "statistics_name", "statistics_owner", "attnames", "exprs", "kinds", "inherited", "nd"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "_NAME", "_TEXT", "_CHAR", "BOOL", "TEXT"},
			[][]any{{"sv_defs", "sv_def_renamed", "sv_defs", "sv_def_moved", "sv_def_owner", "{b}", `{"(renamed + 1)"}`, "{d,f,m,e}", false, `{"3, -1": 1}`}}))
		s.run(fixture.Select("SELECT schemaname,tablename,statistics_schemaname,statistics_owner,expr,most_common_vals::text AS vals FROM pg_stats_ext_exprs WHERE statistics_name='sv_def_moved'", []string{"schemaname", "tablename", "statistics_schemaname", "statistics_owner", "expr", "vals"}, []string{"NAME", "NAME", "NAME", "NAME", "TEXT", "TEXT"}, [][]any{{"sv_defs", "sv_def_renamed", "sv_defs", "sv_def_owner", "(renamed + 1)", "{2}"}}))
		s.run(fixture.Select("SELECT schemaname,statistics_schemaname FROM pg_stats_ext WHERE statistics_name='sv_def_columns'", []string{"schemaname", "statistics_schemaname"}, []string{"NAME", "NAME"}, [][]any{{"sv_defs", "public"}}))
		s.run(fixture.Select("SELECT s.stxowner<>c.relowner AS separate_owners FROM pg_statistic_ext s JOIN pg_class c ON c.oid=s.stxrelid WHERE s.stxname='sv_def_moved'", []string{"separate_owners"}, []string{"BOOL"}, [][]any{{true}}))
		s.exec("DROP STATISTICS sv_defs.sv_def_moved", "CREATE STATISTICS sv_defs.sv_def_moved ON b,(renamed+1) FROM sv_defs.sv_def_renamed")
		s.run(fixture.Select("SELECT oid::bigint<>$1 AS new_identity FROM pg_statistic_ext WHERE stxname='sv_def_moved'", []string{"new_identity"}, []string{"BOOL"}, [][]any{{true}}, id))
		s.run(statisticsViewsCount("pg_stats_ext", "statistics_name='sv_def_moved'", 0))
		s.run(fixture.Select("SELECT expr,inherited,null_frac FROM pg_stats_ext_exprs WHERE statistics_name='sv_def_moved'", []string{"expr", "inherited", "null_frac"}, []string{"TEXT", "BOOL", "FLOAT4"}, [][]any{{"(renamed + 1)", nil, nil}}))

		s.exec(`CREATE TABLE sv_render(a INT,b INT,"Odd Col" INT)`, "CREATE STATISTICS sv_render_1 ON (a+1) FROM sv_render", "CREATE STATISTICS sv_render_2 ON (b::text) FROM sv_render", "CREATE STATISTICS sv_render_3 ON (abs(a)) FROM sv_render", "CREATE STATISTICS sv_render_4 ON (coalesce(a,0)) FROM sv_render", "CREATE STATISTICS sv_render_5 ON (CASE WHEN a>0 THEN a ELSE 0 END) FROM sv_render", `CREATE STATISTICS sv_render_6 ON ("Odd Col"+2) FROM sv_render`)
		// Source-derived PRETTYFLAG_INDENT expectations, not CREATE STATISTICS'
		// pretty-parenthesis mode. The pinned oracle resolves these spellings.
		s.run(fixture.Select("SELECT statistics_name,expr FROM pg_stats_ext_exprs WHERE tablename='sv_render' ORDER BY statistics_name", []string{"statistics_name", "expr"}, []string{"NAME", "TEXT"},
			[][]any{{"sv_render_1", "(a + 1)"}, {"sv_render_2", "(b)::text"}, {"sv_render_3", "abs(a)"}, {"sv_render_4", "COALESCE(a, 0)"}, {"sv_render_5", "\nCASE\n    WHEN (a > 0) THEN a\n    ELSE 0\nEND"}, {"sv_render_6", `("Odd Col" + 2)`}}))
		statisticsViewsViewDefinitions(t, s)
	}, "sv_def_owner")
}

func statisticsViewsViewDefinitions(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	for _, definition := range []struct {
		name           string
		normal, pretty string
	}{
		{"pg_stats", attributeStatsViewDefaultFalse, attributeStatsViewDefaultTrue},
		{"pg_stats_ext", statisticsViewsExtDefinition, statisticsViewsExtPrettyDefinition},
		{"pg_stats_ext_exprs", statisticsViewsExprDefinition, statisticsViewsExprPrettyDefinition},
	} {
		t.Run("viewdef_"+definition.name, func(t *testing.T) {
			inventory := fixture.Select("SELECT schemaname,viewname,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname=$1", []string{"schemaname", "viewname", "definition"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"pg_catalog", definition.name, definition.normal}}, definition.name)
			inventory.Prepared = s.prepared
			fixture.RunStep(t, s.pair.Ctx, s.conn, s.backend.Recorder, inventory)
			for _, pretty := range []bool{false, true} {
				want := definition.normal
				if pretty {
					want = definition.pretty
				}
				step := fixture.Select("SELECT pg_get_viewdef($1::regclass,$2::boolean) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, "pg_catalog."+definition.name, pretty)
				step.Prepared = s.prepared
				fixture.RunStep(t, s.pair.Ctx, s.conn, s.backend.Recorder, step)
			}
		})
	}
}

// These are independently reasoned REL_18_6 definitions. They are literal full
// contracts (including casts, joins and predicates), never native-generated text.
// Pinned oracle discovery must settle whitespace/alias spelling before cutover.
const statisticsViewsExtDefinition = ` SELECT cn.nspname AS schemaname,
    c.relname AS tablename,
    sn.nspname AS statistics_schemaname,
    s.stxname AS statistics_name,
    pg_get_userbyid(s.stxowner) AS statistics_owner,
    ( SELECT array_agg(a.attname ORDER BY a.attnum) AS array_agg
           FROM (unnest(s.stxkeys) k(k)
             JOIN pg_attribute a ON (((a.attrelid = s.stxrelid) AND (a.attnum = k.k))))) AS attnames,
    pg_get_statisticsobjdef_expressions(s.oid) AS exprs,
    s.stxkind AS kinds,
    sd.stxdinherit AS inherited,
    sd.stxdndistinct AS n_distinct,
    sd.stxddependencies AS dependencies,
    m.most_common_vals,
    m.most_common_val_nulls,
    m.most_common_freqs,
    m.most_common_base_freqs
   FROM (((((pg_statistic_ext s
     JOIN pg_class c ON ((c.oid = s.stxrelid)))
     JOIN pg_statistic_ext_data sd ON ((s.oid = sd.stxoid)))
     LEFT JOIN pg_namespace cn ON ((cn.oid = c.relnamespace)))
     LEFT JOIN pg_namespace sn ON ((sn.oid = s.stxnamespace)))
     LEFT JOIN LATERAL ( SELECT array_agg(pg_mcv_list_items."values") AS most_common_vals,
            array_agg(pg_mcv_list_items.nulls) AS most_common_val_nulls,
            array_agg(pg_mcv_list_items.frequency) AS most_common_freqs,
            array_agg(pg_mcv_list_items.base_frequency) AS most_common_base_freqs
           FROM pg_mcv_list_items(sd.stxdmcv) pg_mcv_list_items(index, "values", nulls, frequency, base_frequency)) m ON ((sd.stxdmcv IS NOT NULL)))
  WHERE (pg_has_role(c.relowner, 'USAGE'::text) AND ((c.relrowsecurity = false) OR (NOT row_security_active(c.oid))));`

const statisticsViewsExtPrettyDefinition = ` SELECT cn.nspname AS schemaname,
    c.relname AS tablename,
    sn.nspname AS statistics_schemaname,
    s.stxname AS statistics_name,
    pg_get_userbyid(s.stxowner) AS statistics_owner,
    ( SELECT array_agg(a.attname ORDER BY a.attnum) AS array_agg
           FROM unnest(s.stxkeys) k(k)
             JOIN pg_attribute a ON a.attrelid = s.stxrelid AND a.attnum = k.k) AS attnames,
    pg_get_statisticsobjdef_expressions(s.oid) AS exprs,
    s.stxkind AS kinds,
    sd.stxdinherit AS inherited,
    sd.stxdndistinct AS n_distinct,
    sd.stxddependencies AS dependencies,
    m.most_common_vals,
    m.most_common_val_nulls,
    m.most_common_freqs,
    m.most_common_base_freqs
   FROM pg_statistic_ext s
     JOIN pg_class c ON c.oid = s.stxrelid
     JOIN pg_statistic_ext_data sd ON s.oid = sd.stxoid
     LEFT JOIN pg_namespace cn ON cn.oid = c.relnamespace
     LEFT JOIN pg_namespace sn ON sn.oid = s.stxnamespace
     LEFT JOIN LATERAL ( SELECT array_agg(pg_mcv_list_items."values") AS most_common_vals,
            array_agg(pg_mcv_list_items.nulls) AS most_common_val_nulls,
            array_agg(pg_mcv_list_items.frequency) AS most_common_freqs,
            array_agg(pg_mcv_list_items.base_frequency) AS most_common_base_freqs
           FROM pg_mcv_list_items(sd.stxdmcv) pg_mcv_list_items(index, "values", nulls, frequency, base_frequency)) m ON sd.stxdmcv IS NOT NULL
  WHERE pg_has_role(c.relowner, 'USAGE'::text) AND (c.relrowsecurity = false OR NOT row_security_active(c.oid));`

const statisticsViewsExprDefinition = ` SELECT cn.nspname AS schemaname,
    c.relname AS tablename,
    sn.nspname AS statistics_schemaname,
    s.stxname AS statistics_name,
    pg_get_userbyid(s.stxowner) AS statistics_owner,
    stat.expr,
    sd.stxdinherit AS inherited,
    (stat.a).stanullfrac AS null_frac,
    (stat.a).stawidth AS avg_width,
    (stat.a).stadistinct AS n_distinct,
        CASE
            WHEN ((stat.a).stakind1 = 1) THEN (stat.a).stavalues1
            WHEN ((stat.a).stakind2 = 1) THEN (stat.a).stavalues2
            WHEN ((stat.a).stakind3 = 1) THEN (stat.a).stavalues3
            WHEN ((stat.a).stakind4 = 1) THEN (stat.a).stavalues4
            WHEN ((stat.a).stakind5 = 1) THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS most_common_vals,
        CASE
            WHEN ((stat.a).stakind1 = 1) THEN (stat.a).stanumbers1
            WHEN ((stat.a).stakind2 = 1) THEN (stat.a).stanumbers2
            WHEN ((stat.a).stakind3 = 1) THEN (stat.a).stanumbers3
            WHEN ((stat.a).stakind4 = 1) THEN (stat.a).stanumbers4
            WHEN ((stat.a).stakind5 = 1) THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS most_common_freqs,
        CASE
            WHEN ((stat.a).stakind1 = 2) THEN (stat.a).stavalues1
            WHEN ((stat.a).stakind2 = 2) THEN (stat.a).stavalues2
            WHEN ((stat.a).stakind3 = 2) THEN (stat.a).stavalues3
            WHEN ((stat.a).stakind4 = 2) THEN (stat.a).stavalues4
            WHEN ((stat.a).stakind5 = 2) THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS histogram_bounds,
        CASE
            WHEN ((stat.a).stakind1 = 3) THEN (stat.a).stanumbers1[1]
            WHEN ((stat.a).stakind2 = 3) THEN (stat.a).stanumbers2[1]
            WHEN ((stat.a).stakind3 = 3) THEN (stat.a).stanumbers3[1]
            WHEN ((stat.a).stakind4 = 3) THEN (stat.a).stanumbers4[1]
            WHEN ((stat.a).stakind5 = 3) THEN (stat.a).stanumbers5[1]
            ELSE NULL::real
        END AS correlation,
        CASE
            WHEN ((stat.a).stakind1 = 4) THEN (stat.a).stavalues1
            WHEN ((stat.a).stakind2 = 4) THEN (stat.a).stavalues2
            WHEN ((stat.a).stakind3 = 4) THEN (stat.a).stavalues3
            WHEN ((stat.a).stakind4 = 4) THEN (stat.a).stavalues4
            WHEN ((stat.a).stakind5 = 4) THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS most_common_elems,
        CASE
            WHEN ((stat.a).stakind1 = 4) THEN (stat.a).stanumbers1
            WHEN ((stat.a).stakind2 = 4) THEN (stat.a).stanumbers2
            WHEN ((stat.a).stakind3 = 4) THEN (stat.a).stanumbers3
            WHEN ((stat.a).stakind4 = 4) THEN (stat.a).stanumbers4
            WHEN ((stat.a).stakind5 = 4) THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS most_common_elem_freqs,
        CASE
            WHEN ((stat.a).stakind1 = 5) THEN (stat.a).stanumbers1
            WHEN ((stat.a).stakind2 = 5) THEN (stat.a).stanumbers2
            WHEN ((stat.a).stakind3 = 5) THEN (stat.a).stanumbers3
            WHEN ((stat.a).stakind4 = 5) THEN (stat.a).stanumbers4
            WHEN ((stat.a).stakind5 = 5) THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS elem_count_histogram
   FROM (((((pg_statistic_ext s
     JOIN pg_class c ON ((c.oid = s.stxrelid)))
     LEFT JOIN pg_statistic_ext_data sd ON ((s.oid = sd.stxoid)))
     LEFT JOIN pg_namespace cn ON ((cn.oid = c.relnamespace)))
     LEFT JOIN pg_namespace sn ON ((sn.oid = s.stxnamespace)))
     JOIN LATERAL ( SELECT unnest(pg_get_statisticsobjdef_expressions(s.oid)) AS expr,
            unnest(sd.stxdexpr) AS a) stat ON ((stat.expr IS NOT NULL)))
  WHERE (pg_has_role(c.relowner, 'USAGE'::text) AND ((c.relrowsecurity = false) OR (NOT row_security_active(c.oid))));`

const statisticsViewsExprPrettyDefinition = ` SELECT cn.nspname AS schemaname,
    c.relname AS tablename,
    sn.nspname AS statistics_schemaname,
    s.stxname AS statistics_name,
    pg_get_userbyid(s.stxowner) AS statistics_owner,
    stat.expr,
    sd.stxdinherit AS inherited,
    (stat.a).stanullfrac AS null_frac,
    (stat.a).stawidth AS avg_width,
    (stat.a).stadistinct AS n_distinct,
        CASE
            WHEN (stat.a).stakind1 = 1 THEN (stat.a).stavalues1
            WHEN (stat.a).stakind2 = 1 THEN (stat.a).stavalues2
            WHEN (stat.a).stakind3 = 1 THEN (stat.a).stavalues3
            WHEN (stat.a).stakind4 = 1 THEN (stat.a).stavalues4
            WHEN (stat.a).stakind5 = 1 THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS most_common_vals,
        CASE
            WHEN (stat.a).stakind1 = 1 THEN (stat.a).stanumbers1
            WHEN (stat.a).stakind2 = 1 THEN (stat.a).stanumbers2
            WHEN (stat.a).stakind3 = 1 THEN (stat.a).stanumbers3
            WHEN (stat.a).stakind4 = 1 THEN (stat.a).stanumbers4
            WHEN (stat.a).stakind5 = 1 THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS most_common_freqs,
        CASE
            WHEN (stat.a).stakind1 = 2 THEN (stat.a).stavalues1
            WHEN (stat.a).stakind2 = 2 THEN (stat.a).stavalues2
            WHEN (stat.a).stakind3 = 2 THEN (stat.a).stavalues3
            WHEN (stat.a).stakind4 = 2 THEN (stat.a).stavalues4
            WHEN (stat.a).stakind5 = 2 THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS histogram_bounds,
        CASE
            WHEN (stat.a).stakind1 = 3 THEN (stat.a).stanumbers1[1]
            WHEN (stat.a).stakind2 = 3 THEN (stat.a).stanumbers2[1]
            WHEN (stat.a).stakind3 = 3 THEN (stat.a).stanumbers3[1]
            WHEN (stat.a).stakind4 = 3 THEN (stat.a).stanumbers4[1]
            WHEN (stat.a).stakind5 = 3 THEN (stat.a).stanumbers5[1]
            ELSE NULL::real
        END AS correlation,
        CASE
            WHEN (stat.a).stakind1 = 4 THEN (stat.a).stavalues1
            WHEN (stat.a).stakind2 = 4 THEN (stat.a).stavalues2
            WHEN (stat.a).stakind3 = 4 THEN (stat.a).stavalues3
            WHEN (stat.a).stakind4 = 4 THEN (stat.a).stavalues4
            WHEN (stat.a).stakind5 = 4 THEN (stat.a).stavalues5
            ELSE NULL::anyarray
        END AS most_common_elems,
        CASE
            WHEN (stat.a).stakind1 = 4 THEN (stat.a).stanumbers1
            WHEN (stat.a).stakind2 = 4 THEN (stat.a).stanumbers2
            WHEN (stat.a).stakind3 = 4 THEN (stat.a).stanumbers3
            WHEN (stat.a).stakind4 = 4 THEN (stat.a).stanumbers4
            WHEN (stat.a).stakind5 = 4 THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS most_common_elem_freqs,
        CASE
            WHEN (stat.a).stakind1 = 5 THEN (stat.a).stanumbers1
            WHEN (stat.a).stakind2 = 5 THEN (stat.a).stanumbers2
            WHEN (stat.a).stakind3 = 5 THEN (stat.a).stanumbers3
            WHEN (stat.a).stakind4 = 5 THEN (stat.a).stanumbers4
            WHEN (stat.a).stakind5 = 5 THEN (stat.a).stanumbers5
            ELSE NULL::real[]
        END AS elem_count_histogram
   FROM pg_statistic_ext s
     JOIN pg_class c ON c.oid = s.stxrelid
     LEFT JOIN pg_statistic_ext_data sd ON s.oid = sd.stxoid
     LEFT JOIN pg_namespace cn ON cn.oid = c.relnamespace
     LEFT JOIN pg_namespace sn ON sn.oid = s.stxnamespace
     JOIN LATERAL ( SELECT unnest(pg_get_statisticsobjdef_expressions(s.oid)) AS expr,
            unnest(sd.stxdexpr) AS a) stat ON stat.expr IS NOT NULL
  WHERE pg_has_role(c.relowner, 'USAGE'::text) AND (c.relrowsecurity = false OR NOT row_security_active(c.oid));`
