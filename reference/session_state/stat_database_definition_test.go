package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent REL_18_6 pg_get_viewdef expression inventory. Its one outer FROM
// item leaves oid/datname unqualified, unlike the system_views.sql source.
// Whitespace and redundant parentheses differ between default/pretty forms.
const statDatabaseReleasedDefinition = `SELECT oid AS datid,datname,
 CASE WHEN oid = 0::oid THEN 0 ELSE pg_stat_get_db_numbackends(oid) END AS numbackends,
 pg_stat_get_db_xact_commit(oid) AS xact_commit,
 pg_stat_get_db_xact_rollback(oid) AS xact_rollback,
 pg_stat_get_db_blocks_fetched(oid) - pg_stat_get_db_blocks_hit(oid) AS blks_read,
 pg_stat_get_db_blocks_hit(oid) AS blks_hit,
 pg_stat_get_db_tuples_returned(oid) AS tup_returned,
 pg_stat_get_db_tuples_fetched(oid) AS tup_fetched,
 pg_stat_get_db_tuples_inserted(oid) AS tup_inserted,
 pg_stat_get_db_tuples_updated(oid) AS tup_updated,
 pg_stat_get_db_tuples_deleted(oid) AS tup_deleted,
 pg_stat_get_db_conflict_all(oid) AS conflicts,
 pg_stat_get_db_temp_files(oid) AS temp_files,
 pg_stat_get_db_temp_bytes(oid) AS temp_bytes,
 pg_stat_get_db_deadlocks(oid) AS deadlocks,
 pg_stat_get_db_checksum_failures(oid) AS checksum_failures,
 pg_stat_get_db_checksum_last_failure(oid) AS checksum_last_failure,
 pg_stat_get_db_blk_read_time(oid) AS blk_read_time,
 pg_stat_get_db_blk_write_time(oid) AS blk_write_time,
 pg_stat_get_db_session_time(oid) AS session_time,
 pg_stat_get_db_active_time(oid) AS active_time,
 pg_stat_get_db_idle_in_transaction_time(oid) AS idle_in_transaction_time,
 pg_stat_get_db_sessions(oid) AS sessions,
 pg_stat_get_db_sessions_abandoned(oid) AS sessions_abandoned,
 pg_stat_get_db_sessions_fatal(oid) AS sessions_fatal,
 pg_stat_get_db_sessions_killed(oid) AS sessions_killed,
 pg_stat_get_db_parallel_workers_to_launch(oid) AS parallel_workers_to_launch,
 pg_stat_get_db_parallel_workers_launched(oid) AS parallel_workers_launched,
 pg_stat_get_db_stat_reset_time(oid) AS stats_reset
 FROM (SELECT 0 AS oid,NULL::name AS datname UNION ALL SELECT pg_database.oid,pg_database.datname FROM pg_database) d`

func statDatabaseDefinitionTokens(text string) string {
	// Preserve token/order/identifier/operator inventory; only deparser layout and
	// redundant grouping are normalized, not names, casts, literals or expressions.
	text = strings.NewReplacer("(", " ", ")", " ", ",", " , ", ";", "", "::", " :: ").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}
func TestReferenceStatDatabaseDefinition(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		read := func(pretty bool) string {
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_database'::regclass,$1::boolean)", pretty).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			return definition
		}
		want := statDatabaseDefinitionTokens(statDatabaseReleasedDefinition)
		for _, pretty := range []bool{false, true} {
			definition := read(pretty)
			if got := statDatabaseDefinitionTokens(definition); got != want {
				t.Fatalf("released definition pretty=%t raw:\n%s\nnormalized:\n%s\nwant:\n%s", pretty, definition, got, want)
			}
		}
		statsExec(t, p, b, c, "SET quote_all_identifiers=on", 0)
		for _, pretty := range []bool{false, true} {
			definition := read(pretty)
			for _, fragment := range []string{`"oid"`, `"pg_stat_get_db_xact_commit"("oid")`, `"checksum_last_failure"`, `"parallel_workers_launched"`, `"pg_database"`, `"d"`} {
				if !strings.Contains(definition, fragment) {
					t.Fatal("non-symbolic identifier", pretty, fragment, definition)
				}
			}
		}
		statsExec(t, p, b, c, "SET quote_all_identifiers=off", 0)
		statsExec(t, p, b, c, "CREATE FUNCTION public.pg_stat_get_db_xact_commit(oid) RETURNS bigint LANGUAGE SQL IMMUTABLE AS 'SELECT 1::bigint'", 0)
		statsExec(t, p, b, c, "SET search_path=public,pg_catalog", 0)
		if definition := read(true); !strings.Contains(definition, "pg_catalog.pg_stat_get_db_xact_commit(oid)") {
			t.Fatal("exact signature masking not honored", definition)
		}
		statsExec(t, p, b, c, "RESET search_path", 0)
	})
}
