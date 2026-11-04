package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsShape(t *testing.T) {
	statSubscriptionScenario(t, []string{"StatsMixedλ"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{statSubscriptionStatsProjection(), "SELECT * FROM pg_stat_subscription_stats", statSubscriptionStatsSQL + " WHERE FALSE"} {
			held := statSubscriptionHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				statSubscriptionStatsCheck(t, p, b, executor, query, nil)
			}
		}
		statSubscriptionCreate(t, p, b, c, `"StatsMixedλ"`)
		id := statSubscriptionID(t, p, c, "StatsMixedλ")
		for _, query := range []string{statSubscriptionStatsSQL + " WHERE subid=$1::oid", statSubscriptionStatsProjection() + " WHERE subid=$1::oid"} {
			h := statSubscriptionHeld(t, p, c, query)
			for _, e := range []fixture.Executor{c, h, h} {
				statSubscriptionStatsCheck(t, p, b, e, query, [][]any{statSubscriptionStatsRow(id, "StatsMixedλ")}, id)
			}
			h.Statement.Close()
		}
		statSubscriptionExec(t, p, b, c, "SET search_path=public")
		statSubscriptionStatsCheck(t, p, b, c, "SELECT * FROM pg_stat_subscription_stats WHERE FALSE", nil)
		var attributes [][]any
		for i, name := range statSubscriptionStatsNames {
			collation := int64(0)
			if i == 1 {
				collation = 950
			}
			attributes = append(attributes, []any{int64(i + 1), name, statSubscriptionStatsTypeIDs[i], false, int64(-1), collation, int64(0)})
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres" // The pinned fixture's initdb bootstrap superuser.
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_subscription_stats'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes),
			fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_subscription_stats'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(12), true, true, true}}),
			fixture.Select("SELECT n.nspname,r.rolname,c.relowner=(SELECT relowner FROM pg_class WHERE oid='pg_catalog.pg_class'::regclass) AS bootstrap_owner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='pg_catalog.pg_stat_subscription_stats'::regclass", []string{"nspname", "rolname", "bootstrap_owner"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"pg_catalog", owner, true}}),
			fixture.Select("SELECT rulename,ev_type,is_instead FROM pg_rewrite WHERE ev_class='pg_catalog.pg_stat_subscription_stats'::regclass", []string{"rulename", "ev_type", "is_instead"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", true}}),
			fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='pg_catalog.pg_stat_subscription_stats'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid=r.ev_class AND d.deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
		} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		statSubscriptionStatsRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_subscription_stats'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12347), int64(12349), int64(12348)}}))
		}
		const definition = ` SELECT ss.subid,
    s.subname,
    ss.apply_error_count,
    ss.sync_error_count,
    ss.confl_insert_exists,
    ss.confl_update_origin_differs,
    ss.confl_update_exists,
    ss.confl_update_missing,
    ss.confl_delete_origin_differs,
    ss.confl_delete_missing,
    ss.confl_multiple_unique_conflicts,
    ss.stats_reset
   FROM pg_subscription s,
    LATERAL pg_stat_get_subscription_stats(s.oid) ss(subid, apply_error_count, sync_error_count, confl_insert_exists, confl_update_origin_differs, confl_update_exists, confl_update_missing, confl_delete_origin_differs, confl_delete_missing, confl_multiple_unique_conflicts, stats_reset);`
		const prettyDefinition = definition
		for _, tc := range []struct{ query, definition string }{
			{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_subscription_stats'", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_subscription_stats'::regclass) AS definition", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_subscription_stats'::regclass,true) AS definition", prettyDefinition},
		} {
			step := fixture.Select(tc.query, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
	})
}

func statSubscriptionStatsRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid=6231",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(6231), "pg_stat_get_subscription_stats", int64(11), int64(12), float64(1), float64(0), "f", false, false, true, false, "s", "r", int64(1), int64(0), int64(2249), "26", "{26,26,20,20,20,20,20,20,20,20,20,1184}", "{i,o,o,o,o,o,o,o,o,o,o,o}", "{subid,subid,apply_error_count,sync_error_count,confl_insert_exists,confl_update_origin_differs,confl_update_exists,confl_update_missing,confl_delete_origin_differs,confl_delete_missing,confl_multiple_unique_conflicts,stats_reset}", "pg_stat_get_subscription_stats", nil, nil, nil}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(step))
}
