package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionShape(t *testing.T) {
	statSubscriptionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{statSubscriptionProjection(), "SELECT * FROM pg_stat_subscription", statSubscriptionSQL + " WHERE FALSE"} {
			held := statSubscriptionHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				statSubscriptionCheck(t, p, b, executor, query, nil)
			}
		}
		var attributes [][]any
		for i, name := range statSubscriptionNames {
			collation := int64(0)
			if i == 1 {
				collation = 950
			} else if i == 2 {
				collation = 100
			}
			attributes = append(attributes, []any{int64(i + 1), name, statSubscriptionTypeIDs[i], false, int64(-1), collation, int64(0)})
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres" // The pinned fixture's initdb bootstrap superuser.
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_subscription'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes),
			fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_subscription'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), true, true, true}}),
			fixture.Select("SELECT n.nspname,r.rolname,c.relowner=(SELECT relowner FROM pg_class WHERE oid='pg_catalog.pg_class'::regclass) AS bootstrap_owner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='pg_catalog.pg_stat_subscription'::regclass", []string{"nspname", "rolname", "bootstrap_owner"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"pg_catalog", owner, true}}),
			fixture.Select("SELECT rulename,ev_type,is_instead FROM pg_rewrite WHERE ev_class='pg_catalog.pg_stat_subscription'::regclass", []string{"rulename", "ev_type", "is_instead"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", true}}),
		} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		statSubscriptionRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_subscription'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12248), int64(12250), int64(12249)}}))
		}
		const definition = ` SELECT su.oid AS subid,
    su.subname,
    st.worker_type,
    st.pid,
    st.leader_pid,
    st.relid,
    st.received_lsn,
    st.last_msg_send_time,
    st.last_msg_receipt_time,
    st.latest_end_lsn,
    st.latest_end_time
   FROM (pg_subscription su
     LEFT JOIN pg_stat_get_subscription(NULL::oid) st(subid, relid, pid, leader_pid, received_lsn, last_msg_send_time, last_msg_receipt_time, latest_end_lsn, latest_end_time, worker_type) ON ((st.subid = su.oid)));`
		const prettyDefinition = ` SELECT su.oid AS subid,
    su.subname,
    st.worker_type,
    st.pid,
    st.leader_pid,
    st.relid,
    st.received_lsn,
    st.last_msg_send_time,
    st.last_msg_receipt_time,
    st.latest_end_lsn,
    st.latest_end_time
   FROM pg_subscription su
     LEFT JOIN pg_stat_get_subscription(NULL::oid) st(subid, relid, pid, leader_pid, received_lsn, last_msg_send_time, last_msg_receipt_time, latest_end_lsn, latest_end_time, worker_type) ON st.subid = su.oid;`
		for _, tc := range []struct{ query, definition string }{
			{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_subscription'", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_subscription'::regclass) AS definition", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_subscription'::regclass,true) AS definition", prettyDefinition},
		} {
			step := fixture.Select(tc.query, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
	})
}

func statSubscriptionRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid=6118",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(6118), "pg_stat_get_subscription", int64(11), int64(12), float64(1), float64(10), "f", false, false, false, true, "s", "r", int64(1), int64(0), int64(2249), "26", "{26,26,26,23,23,3220,1184,1184,3220,1184,25}", "{i,o,o,o,o,o,o,o,o,o,o}", "{subid,subid,relid,pid,leader_pid,received_lsn,last_msg_send_time,last_msg_receipt_time,latest_end_lsn,latest_end_time,worker_type}", "pg_stat_get_subscription", nil, nil, nil}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(step))
}
