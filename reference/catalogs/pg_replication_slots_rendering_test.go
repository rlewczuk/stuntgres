package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 system_views.sql's join, in released ruleutils display form.
const slotsDefinition = " SELECT l.slot_name,\n    l.plugin,\n    l.slot_type,\n    l.datoid,\n    d.datname AS database,\n    l.temporary,\n    l.active,\n    l.active_pid,\n    l.xmin,\n    l.catalog_xmin,\n    l.restart_lsn,\n    l.confirmed_flush_lsn,\n    l.wal_status,\n    l.safe_wal_size,\n    l.two_phase,\n    l.two_phase_at,\n    l.inactive_since,\n    l.conflicting,\n    l.invalidation_reason,\n    l.failover,\n    l.synced\n   FROM (pg_get_replication_slots() l(slot_name, plugin, slot_type, datoid, temporary, active, active_pid, xmin, catalog_xmin, restart_lsn, confirmed_flush_lsn, wal_status, safe_wal_size, two_phase, two_phase_at, inactive_since, conflicting, invalidation_reason, failover, synced)\n     LEFT JOIN pg_database d ON ((l.datoid = d.oid)));"

func TestReferencePGReplicationSlotsRendering(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			originStatusCleanup(t, c, "RESET search_path", "RESET quote_all_identifiers")
			check := func(definition string) {
				pretty := strings.Replace(definition, "FROM (", "FROM ", 1)
				pretty = strings.Replace(pretty, "ON ((l.datoid = d.oid)))", "ON l.datoid = d.oid", 1)
				pretty = strings.Replace(pretty, "ON ((\"l\".\"datoid\" = \"d\".\"oid\")))", "ON \"l\".\"datoid\" = \"d\".\"oid\"", 1)
				s := fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_replication_slots'::regclass,false) AS nonpretty,pg_get_viewdef('pg_catalog.pg_replication_slots'::regclass,true) AS pretty,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_replication_slots'", []string{"nonpretty", "pretty", "definition"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{definition, pretty, definition}})
				slotsRun(t, p, b, c, s)
				slotsRun(t, p, b, c, fixture.Prepared(s))
				slotsEmpty(t, p, b, c, slotsSelect)
			}
			check(slotsDefinition)
			for _, q := range []string{
				"CREATE FUNCTION public.pg_get_replication_slots() RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
				"CREATE TABLE public.pg_database(oid oid,datname name)",
				"SET search_path=public,pg_catalog",
			} {
				slotsRun(t, p, b, c, fixture.Exec(q, 0))
			}
			qualified := strings.Replace(slotsDefinition, "pg_get_replication_slots()", "pg_catalog.pg_get_replication_slots()", 1)
			qualified = strings.Replace(qualified, "LEFT JOIN pg_database", "LEFT JOIN pg_catalog.pg_database", 1)
			check(qualified)
			slotsRun(t, p, b, c, fixture.Exec("SET quote_all_identifiers=on", 0))
			// Quote the independent expected identifier tokens, never native output.
			var quoted strings.Builder
			for i := 0; i < len(qualified); {
				if qualified[i] >= 'a' && qualified[i] <= 'z' || qualified[i] == '_' {
					j := i + 1
					for j < len(qualified) && (qualified[j] >= 'a' && qualified[j] <= 'z' || qualified[j] == '_') {
						j++
					}
					quoted.WriteString("\"" + qualified[i:j] + "\"")
					i = j
				} else {
					quoted.WriteByte(qualified[i])
					i++
				}
			}
			check(quoted.String())
		})
	}
}
