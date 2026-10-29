package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteBootstrapReturnRules(t *testing.T) {
	for _, view := range []string{"pg_stat_progress_copy", "pg_stat_progress_create_index"} {
		t.Run(view, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				var id int64
				q := "SELECT oid::bigint FROM pg_rewrite WHERE ev_class=$1::regclass AND rulename='_RETURN'"
				if err := c.QueryRowContext(p.Ctx, q, "pg_catalog."+view).Scan(&id); err != nil || id <= 0 {
					t.Fatal("advertised bootstrap rule", id, err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT rulename,ev_type,is_instead FROM pg_rewrite WHERE ev_class=$1::regclass", []string{"rulename", "ev_type", "is_instead"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", true}}, "pg_catalog."+view))
				for _, suffix := range []string{"", ",false", ",true"} {
					pretty := suffix == ",true"
					body := copyProgressDefaultDefinition
					if pretty {
						body = copyProgressPrettyDefinition
					}
					if view == "pg_stat_progress_create_index" {
						body = rewriteIndexDefaultBody
						if pretty {
							body = rewriteIndexPrettyBody
						}
					}
					target := "pg_catalog." + view
					if pretty {
						target = view
					}
					want := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO " + target + " DO INSTEAD " + body
					for _, call := range []struct {
						q    string
						args []any
					}{
						{"SELECT pg_get_ruledef(oid" + suffix + ") AS definition FROM pg_rewrite WHERE ev_class=$1::regclass AND rulename='_RETURN'", []any{"pg_catalog." + view}},
						{"SELECT pg_catalog.pg_get_ruledef($1::oid" + suffix + ") AS definition", []any{id}},
					} {
						s := fixture.DeparseRelationsTextStep(t, b, call.q, want, call.args...)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					}
					s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_ruledef($1,$2) AS definition", want, id, pretty)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
				// Exact default/pretty body goldens above are independent. In the
				// quoting/path phase additionally require the view and rule renderers
				// to share the same captured body authority, without modifying it.
				for _, setting := range []string{"SET quote_all_identifiers TO on", "SET search_path TO public,pg_catalog"} {
					if setting == "SET search_path TO public,pg_catalog" {
						for _, ddl := range []string{"CREATE TABLE public." + view + "(pid INT)", "CREATE TABLE public.pg_database(oid INT,datname TEXT)"} {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(setting, 0))
					for _, pretty := range []bool{false, true} {
						body := fixture.DeparseRelationsRead(t, p, b, c, fmt.Sprintf("SELECT pg_get_viewdef($1::regclass,%v) AS definition", pretty), "pg_catalog."+view)
						if !strings.HasPrefix(body, ` SELECT "s"."pid",`) || !strings.HasSuffix(body, ";") {
							t.Fatal("quoted body structure", body)
						}
						if setting == "SET search_path TO public,pg_catalog" && !strings.Contains(body, `LEFT JOIN "pg_catalog"."pg_database"`) {
							t.Fatal("body retargeted to path decoy", body)
						}
						target := `"pg_catalog"."` + view + `"`
						if pretty && setting != "SET search_path TO public,pg_catalog" {
							target = `"` + view + `"`
						}
						want := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO " + target + " DO INSTEAD " + body
						s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_ruledef($1::oid,$2::boolean) AS definition", want, id, pretty)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					}
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET quote_all_identifiers", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET search_path", 0))
			})
		})
	}
}

// Independent released system_views.sql projection/parameter mapping.
// Default and pretty parentheses are pinned by the PostgreSQL 18.6 oracle.
const rewriteIndexProjection = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
    (s.param7)::oid AS index_relid,
        CASE s.param1
            WHEN 1 THEN 'CREATE INDEX'::text
            WHEN 2 THEN 'CREATE INDEX CONCURRENTLY'::text
            WHEN 3 THEN 'REINDEX'::text
            WHEN 4 THEN 'REINDEX CONCURRENTLY'::text
            ELSE NULL::text
        END AS command,
        CASE s.param10
            WHEN 0 THEN 'initializing'::text
            WHEN 1 THEN 'waiting for writers before build'::text
            WHEN 2 THEN ('building index'::text || COALESCE((': '::text || pg_indexam_progress_phasename((s.param9)::oid, s.param11)), ''::text))
            WHEN 3 THEN 'waiting for writers before validation'::text
            WHEN 4 THEN 'index validation: scanning index'::text
            WHEN 5 THEN 'index validation: sorting tuples'::text
            WHEN 6 THEN 'index validation: scanning table'::text
            WHEN 7 THEN 'waiting for old snapshots'::text
            WHEN 8 THEN 'waiting for readers before marking dead'::text
            WHEN 9 THEN 'waiting for readers before dropping'::text
            ELSE NULL::text
        END AS phase,
    s.param4 AS lockers_total,
    s.param5 AS lockers_done,
    s.param6 AS current_locker_pid,
    s.param16 AS blocks_total,
    s.param17 AS blocks_done,
    s.param12 AS tuples_total,
    s.param13 AS tuples_done,
    s.param14 AS partitions_total,
    s.param15 AS partitions_done
`
const rewriteIndexArguments = "('CREATE INDEX'::text) s(pid, datid, relid, param1, param2, param3, param4, param5, param6, param7, param8, param9, param10, param11, param12, param13, param14, param15, param16, param17, param18, param19, param20)\n"
const rewriteIndexDefaultBody = rewriteIndexProjection + "   FROM (pg_stat_get_progress_info" + rewriteIndexArguments + "     LEFT JOIN pg_database d ON ((s.datid = d.oid)));"
const rewriteIndexPrettyBody = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
    s.param7::oid AS index_relid,
        CASE s.param1
            WHEN 1 THEN 'CREATE INDEX'::text
            WHEN 2 THEN 'CREATE INDEX CONCURRENTLY'::text
            WHEN 3 THEN 'REINDEX'::text
            WHEN 4 THEN 'REINDEX CONCURRENTLY'::text
            ELSE NULL::text
        END AS command,
        CASE s.param10
            WHEN 0 THEN 'initializing'::text
            WHEN 1 THEN 'waiting for writers before build'::text
            WHEN 2 THEN 'building index'::text || COALESCE(': '::text || pg_indexam_progress_phasename(s.param9::oid, s.param11), ''::text)
            WHEN 3 THEN 'waiting for writers before validation'::text
            WHEN 4 THEN 'index validation: scanning index'::text
            WHEN 5 THEN 'index validation: sorting tuples'::text
            WHEN 6 THEN 'index validation: scanning table'::text
            WHEN 7 THEN 'waiting for old snapshots'::text
            WHEN 8 THEN 'waiting for readers before marking dead'::text
            WHEN 9 THEN 'waiting for readers before dropping'::text
            ELSE NULL::text
        END AS phase,
    s.param4 AS lockers_total,
    s.param5 AS lockers_done,
    s.param6 AS current_locker_pid,
    s.param16 AS blocks_total,
    s.param17 AS blocks_done,
    s.param12 AS tuples_total,
    s.param13 AS tuples_done,
    s.param14 AS partitions_total,
    s.param15 AS partitions_done
` + "   FROM pg_stat_get_progress_info" + rewriteIndexArguments + "     LEFT JOIN pg_database d ON s.datid = d.oid;"
