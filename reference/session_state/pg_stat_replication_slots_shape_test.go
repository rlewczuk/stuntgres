package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{replicationSlotsSQL, "SELECT * FROM pg_stat_replication_slots", replicationSlotsSQL + " WHERE FALSE"} {
			held := prefetchHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				replicationSlotsMetadata(t, rows, b.PG)
				if rows.Next() || rows.Err() != nil {
					t.Fatal("empty inventory", rows.Err())
				}
				rows.Close()
			}
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT slot_name,total_txns,stats_reset FROM pg_stat_replication_slots", []string{"slot_name", "total_txns", "stats_reset"}, []string{"TEXT", "INT8", "TIMESTAMPTZ"}, nil),
			fixture.Select(replicationSlotsSQL+" WHERE slot_name=$1::text", replicationSlotsNames, replicationSlotsTypes, nil, "absent_slot"),
			fixture.Select("SELECT count(*) AS n FROM pg_stat_replication_slots", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
			fixture.Select("WITH slots AS (SELECT slot_name,total_txns FROM pg_stat_replication_slots) SELECT slot_name,total_txns FROM slots", []string{"slot_name", "total_txns"}, []string{"TEXT", "INT8"}, nil),
			fixture.Select("SELECT slot_name FROM (SELECT slot_name FROM pg_stat_replication_slots) AS slots", []string{"slot_name"}, []string{"TEXT"}, nil),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		var attributes [][]any
		for i, name := range replicationSlotsNames {
			oid, collation := int64(20), int64(0)
			if i == 0 {
				oid, collation = 25, 100
			} else if i == 9 {
				oid = 1184
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_replication_slots'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_replication_slots'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(10), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_replication_slots'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12266), int64(12268), int64(12267)}}))
		}
		const definition = ` SELECT s.slot_name,
    s.spill_txns,
    s.spill_count,
    s.spill_bytes,
    s.stream_txns,
    s.stream_count,
    s.stream_bytes,
    s.total_txns,
    s.total_bytes,
    s.stats_reset
   FROM pg_replication_slots r,
    LATERAL pg_stat_get_replication_slot((r.slot_name)::text) s(slot_name, spill_txns, spill_count, spill_bytes, stream_txns, stream_count, stream_bytes, total_txns, total_bytes, stats_reset)
  WHERE (r.datoid IS NOT NULL);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_replication_slots'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_replication_slots'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := prefetchHeld(t, p, c, replicationSlotsSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, replicationSlotsSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context cancellation", err)
				}
			}
		}
		replicationSlotsEmpty(t, p, b, c)
	})
}
