package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALReceiverShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			query   string
			indices []int
			args    []any
		}{
			{walReceiverSQL, walReceiverAllColumns(), nil},
			{"SELECT * FROM pg_stat_wal_receiver", walReceiverAllColumns(), nil},
			{walReceiverSQL + " WHERE FALSE", walReceiverAllColumns(), nil},
			{walReceiverSQL + " WHERE pid=$1", walReceiverAllColumns(), []any{int64(-1)}},
			{"SELECT pid,receive_start_lsn,last_msg_receipt_time,sender_host,sender_port,conninfo FROM pg_catalog.pg_stat_wal_receiver", []int{0, 2, 8, 12, 13, 14}, nil},
		} {
			step := walReceiverStep(t, b, tc.query, tc.indices, tc.args...)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			held := walReceiverHeld(t, p, c, tc.query)
			defer held.Statement.Close()
			// Fresh and retained executions must expose complete metadata before
			// iteration, even though neither has a receiver row to scan.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			rows, err := held.QueryContext(p.Ctx, tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			walReceiverFinishEmpty(t, rows, b.PG, tc.indices)
		}
		count := fixture.Select("SELECT count(*) FROM pg_stat_wal_receiver", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, count)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(count))
		var attributes [][]any
		for i, name := range walReceiverColumnNames {
			collation := int64(0)
			if walReceiverTypeOIDs[i] == 25 {
				collation = 100
			}
			attributes = append(attributes, []any{int64(i + 1), name, walReceiverTypeOIDs[i], false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_wal_receiver'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.relhasindex,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_wal_receiver'::regclass", []string{"relkind", "relnatts", "relhasindex", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(15), false, true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			identity := fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_wal_receiver'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12240), int64(12242), int64(12241)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, identity)
		}
		// Pinned PostgreSQL 18.6 renders the selected fields without the sole
		// FROM alias. Assert normal and pretty display independently.
		const definition = ` SELECT pid,
    status,
    receive_start_lsn,
    receive_start_tli,
    written_lsn,
    flushed_lsn,
    received_tli,
    last_msg_send_time,
    last_msg_receipt_time,
    latest_end_lsn,
    latest_end_time,
    slot_name,
    sender_host,
    sender_port,
    conninfo
   FROM pg_stat_get_wal_receiver() s(pid, status, receive_start_lsn, receive_start_tli, written_lsn, flushed_lsn, received_tli, last_msg_send_time, last_msg_receipt_time, latest_end_lsn, latest_end_time, slot_name, sender_host, sender_port, conninfo)
  WHERE (pid IS NOT NULL);`
		const prettyDefinition = ` SELECT pid,
    status,
    receive_start_lsn,
    receive_start_tli,
    written_lsn,
    flushed_lsn,
    received_tli,
    last_msg_send_time,
    last_msg_receipt_time,
    latest_end_lsn,
    latest_end_time,
    slot_name,
    sender_host,
    sender_port,
    conninfo
   FROM pg_stat_get_wal_receiver() s(pid, status, receive_start_lsn, receive_start_tli, written_lsn, flushed_lsn, received_tli, last_msg_send_time, last_msg_receipt_time, latest_end_lsn, latest_end_time, slot_name, sender_host, sender_port, conninfo)
  WHERE pid IS NOT NULL;`
		for _, tc := range []struct {
			query, definition string
		}{
			{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_wal_receiver'", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_wal_receiver'::regclass) AS definition", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_wal_receiver'::regclass,false) AS definition", definition},
			{"SELECT pg_get_viewdef('pg_catalog.pg_stat_wal_receiver'::regclass,true) AS definition", prettyDefinition},
		} {
			step := fixture.Select(tc.query, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
	})
}
