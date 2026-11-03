package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			query   string
			indices []int
			args    []any
		}{
			{replicationSQL, replicationAllColumns(), nil},
			{"SELECT * FROM pg_stat_replication", replicationAllColumns(), nil},
			{replicationSQL + " WHERE FALSE", replicationAllColumns(), nil},
			{replicationSQL + " WHERE pid=$1", replicationAllColumns(), []any{int64(-1)}},
			{"SELECT pid,sent_lsn,reply_time FROM pg_catalog.pg_stat_replication", []int{0, 10, 19}, nil},
		} {
			step := replicationStep(t, b, tc.query, tc.indices, tc.args...)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			held := replicationHeld(t, p, c, tc.query)
			defer held.Statement.Close()
			// Fresh and retained executions must expose complete metadata before
			// iteration, even though neither has a sender row to scan.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		count := fixture.Select("SELECT count(*) FROM pg_stat_replication", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, count)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(count))
		var attributes [][]any
		for i, name := range replicationColumnNames {
			collation := int64(0)
			if replicationTypeOIDs[i] == 19 {
				collation = 950
			} else if replicationTypeOIDs[i] == 25 {
				collation = 100
			}
			attributes = append(attributes, []any{int64(i + 1), name, replicationTypeOIDs[i], false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_replication'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_replication'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(20), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			identity := fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_replication'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12231), int64(12233), int64(12232)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, identity)
		}
		// Exact pg_get_viewdef output observed independently on pinned PostgreSQL 18.6.
		const definition = ` SELECT s.pid,
    s.usesysid,
    u.rolname AS usename,
    s.application_name,
    s.client_addr,
    s.client_hostname,
    s.client_port,
    s.backend_start,
    s.backend_xmin,
    w.state,
    w.sent_lsn,
    w.write_lsn,
    w.flush_lsn,
    w.replay_lsn,
    w.write_lag,
    w.flush_lag,
    w.replay_lag,
    w.sync_priority,
    w.sync_state,
    w.reply_time
   FROM ((pg_stat_get_activity(NULL::integer) s(datid, pid, usesysid, application_name, state, query, wait_event_type, wait_event, xact_start, query_start, backend_start, state_change, client_addr, client_hostname, client_port, backend_xid, backend_xmin, backend_type, ssl, sslversion, sslcipher, sslbits, ssl_client_dn, ssl_client_serial, ssl_issuer_dn, gss_auth, gss_princ, gss_enc, gss_delegation, leader_pid, query_id)
     JOIN pg_stat_get_wal_senders() w(pid, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, write_lag, flush_lag, replay_lag, sync_priority, sync_state, reply_time) ON ((s.pid = w.pid)))
     LEFT JOIN pg_authid u ON ((s.usesysid = u.oid)));`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_replication'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_replication'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
	})
}
