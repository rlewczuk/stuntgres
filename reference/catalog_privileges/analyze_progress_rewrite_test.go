package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent PG18 system_views.sql mapping. Whitespace and protocol overloads
// are asserted against the pinned release, not learned from the native backend.
const analyzeProgressProjection = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
        CASE s.param1
            WHEN 0 THEN 'initializing'::text
            WHEN 1 THEN 'acquiring sample rows'::text
            WHEN 2 THEN 'acquiring inherited sample rows'::text
            WHEN 3 THEN 'computing statistics'::text
            WHEN 4 THEN 'computing extended statistics'::text
            WHEN 5 THEN 'finalizing analyze'::text
            ELSE NULL::text
        END AS phase,
    s.param2 AS sample_blks_total,
    s.param3 AS sample_blks_scanned,
    s.param4 AS ext_stats_total,
    s.param5 AS ext_stats_computed,
    s.param6 AS child_tables_total,
    s.param7 AS child_tables_done,
    (s.param8)::oid AS current_child_table_relid,
    ((s.param9)::double precision / (1000000)::double precision) AS delay_time
`
const analyzeProgressArguments = "('ANALYZE'::text) s(pid, datid, relid, param1, param2, param3, param4, param5, param6, param7, param8, param9, param10, param11, param12, param13, param14, param15, param16, param17, param18, param19, param20)\n"
const analyzeProgressDefaultBody = analyzeProgressProjection + "   FROM (pg_stat_get_progress_info" + analyzeProgressArguments + "     LEFT JOIN pg_database d ON ((s.datid = d.oid)));"
const analyzeProgressPrettyBody = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
        CASE s.param1
            WHEN 0 THEN 'initializing'::text
            WHEN 1 THEN 'acquiring sample rows'::text
            WHEN 2 THEN 'acquiring inherited sample rows'::text
            WHEN 3 THEN 'computing statistics'::text
            WHEN 4 THEN 'computing extended statistics'::text
            WHEN 5 THEN 'finalizing analyze'::text
            ELSE NULL::text
        END AS phase,
    s.param2 AS sample_blks_total,
    s.param3 AS sample_blks_scanned,
    s.param4 AS ext_stats_total,
    s.param5 AS ext_stats_computed,
    s.param6 AS child_tables_total,
    s.param7 AS child_tables_done,
    s.param8::oid AS current_child_table_relid,
    s.param9::double precision / 1000000::double precision AS delay_time
` + "   FROM pg_stat_get_progress_info" + analyzeProgressArguments + "     LEFT JOIN pg_database d ON s.datid = d.oid;"

func TestReferenceAnalyzeProgressRewrite(t *testing.T) {
	analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		var oid, rule int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_analyze'::regclass").Scan(&oid); err != nil || oid <= 0 {
			t.Fatalf("view identity %d: %v", oid, err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_rewrite WHERE ev_class=$1::oid AND rulename='_RETURN'", oid).Scan(&rule); err != nil || rule <= 0 {
			t.Fatalf("rule identity %d: %v", rule, err)
		}
		for _, pretty := range []bool{false, true} {
			body := analyzeProgressDefaultBody
			if pretty {
				body = analyzeProgressPrettyBody
			}
			for _, call := range []struct {
				q    string
				args []any
			}{
				{"SELECT pg_get_viewdef($1::oid,$2::boolean) AS definition", []any{oid, pretty}},
				{"SELECT pg_catalog.pg_get_viewdef($1::text,$2::boolean) AS definition", []any{"pg_catalog.pg_stat_progress_analyze", pretty}},
				{fmt.Sprintf("SELECT pg_get_viewdef('pg_catalog.pg_stat_progress_analyze'::regclass,%v) AS definition", pretty), nil},
			} {
				s := fixture.DeparseRelationsTextStep(t, b, call.q, body, call.args...)
				vacuumRun(t, p, b, c, s)
				vacuumRun(t, p, b, c, fixture.Prepared(s))
			}
			target := "pg_catalog.pg_stat_progress_analyze"
			if pretty {
				target = "pg_stat_progress_analyze"
			}
			s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_ruledef($1::oid,$2::boolean) AS definition", "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO "+target+" DO INSTEAD "+body, rule, pretty)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		for _, call := range []struct {
			q   string
			arg any
		}{
			{"SELECT pg_get_viewdef($1::oid) AS definition", oid},
			{"SELECT pg_get_viewdef($1::text) AS definition", "pg_catalog.pg_stat_progress_analyze"},
		} {
			s := fixture.DeparseRelationsTextStep(t, b, call.q, analyzeProgressDefaultBody, call.arg)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		wrap40 := strings.Replace(analyzeProgressPrettyBody, " SELECT s.pid,\n    s.datid,\n    d.datname,", " SELECT s.pid, s.datid, d.datname,", 1)
		s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_viewdef($1::oid,40) AS definition", wrap40, oid)
		vacuumRun(t, p, b, c, s)
		vacuumRun(t, p, b, c, fixture.Prepared(s))
		for _, q := range []string{
			"CREATE TABLE public.pg_stat_progress_analyze(pid INT)",
			"CREATE TABLE public.pg_database(oid INT,datname TEXT)",
			"SET search_path TO public,pg_catalog",
			"SET quote_all_identifiers TO on",
		} {
			copyProgressExec(t, p.Ctx, c, q)
		}
		for _, pretty := range []bool{false, true} {
			body := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef($1::oid,$2::boolean) AS definition", oid, pretty)
			for _, token := range []string{` SELECT "s"."pid",`, `LEFT JOIN "pg_catalog"."pg_database"`, `"s"."param8"`, `"s"."param9"`, `"current_child_table_relid"`, `"delay_time"`} {
				if !strings.Contains(body, token) {
					t.Fatalf("quoted structural token %s absent: %s", token, body)
				}
			}
			for i := 1; i <= 20; i++ {
				if !strings.Contains(body, fmt.Sprintf(`"param%d"`, i)) {
					t.Fatalf("lost progress alias %d: %s", i, body)
				}
			}
			s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_ruledef($1::oid,$2::boolean) AS definition", "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO \"pg_catalog\".\"pg_stat_progress_analyze\" DO INSTEAD "+body, rule, pretty)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		copyProgressExec(t, p.Ctx, c, "RESET quote_all_identifiers")
		copyProgressExec(t, p.Ctx, c, "RESET search_path")
	})
}
