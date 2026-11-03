package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatCheckpointerShape(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := checkpointerGet(t, p, b, c)
		for _, query := range []string{checkpointerSQL, "SELECT * FROM pg_stat_checkpointer", checkpointerSQL + " WHERE FALSE"} {
			held := checkpointerHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == checkpointerSQL+" WHERE FALSE" {
					checkpointerMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					checkpointerSameReset(t, before, checkpointerRead(t, rows, b.PG))
				}
			}
		}
		for _, query := range []string{"SELECT stats_reset,slru_written,buffers_written,write_time,sync_time,num_timed,num_timed,restartpoints_done FROM pg_stat_checkpointer", "SELECT stats_reset,slru_written,buffers_written,write_time,sync_time,num_timed,num_timed,restartpoints_done FROM pg_catalog.pg_stat_checkpointer"} {
			selected := checkpointerHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected, selected} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				names, err := rows.Columns()
				want := []string{"stats_reset", "slru_written", "buffers_written", "write_time", "sync_time", "num_timed", "num_timed", "restartpoints_done"}
				if err != nil || !reflect.DeepEqual(names, want) {
					t.Fatal("reordered/repeated names", names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 8 {
					t.Fatal("reordered descriptors", types, err)
				}
				for i, typ := range types {
					expected := "INT8"
					if i == 0 {
						expected = "TIMESTAMPTZ"
					}
					if i == 3 || i == 4 {
						expected = "FLOAT8"
					}
					if typ.DatabaseTypeName() != expected {
						t.Fatal("reordered type", i, typ.DatabaseTypeName())
					}
				}
				if !rows.Next() {
					t.Fatal("missing reordered singleton", rows.Err())
				}
				var at time.Time
				var slru, buffers, timed, repeated, restart int64
				var write, sync float64
				if err := rows.Scan(&at, &slru, &buffers, &write, &sync, &timed, &repeated, &restart); err != nil || !at.Equal(before.reset) || timed != repeated || slru < 0 || buffers < 0 || write < 0 || sync < 0 || timed < 0 || restart < 0 {
					t.Fatal("reordered values", at, slru, buffers, write, sync, timed, repeated, restart, err)
				}
				if !b.PG && (slru != 0 || buffers != 0 || write != 0 || sync != 0 || timed != 0 || restart != 0) {
					t.Fatal("fabricated physical activity")
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra reordered row", rows.Err())
				}
				rows.Close()
			}
			selected.Statement.Close()
		}
		var attributes [][]any
		for i, name := range checkpointerNames {
			oid, collation := int64(1184), int64(0)
			if i != 10 {
				oid = 20
			}
			if i == 6 || i == 7 {
				oid = 701
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_checkpointer'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_checkpointer'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		checkpointerRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_checkpointer'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12297), int64(12299), int64(12298)}}))
		}
		const definition = ` SELECT pg_stat_get_checkpointer_num_timed() AS num_timed,
    pg_stat_get_checkpointer_num_requested() AS num_requested,
    pg_stat_get_checkpointer_num_performed() AS num_done,
    pg_stat_get_checkpointer_restartpoints_timed() AS restartpoints_timed,
    pg_stat_get_checkpointer_restartpoints_requested() AS restartpoints_req,
    pg_stat_get_checkpointer_restartpoints_performed() AS restartpoints_done,
    pg_stat_get_checkpointer_write_time() AS write_time,
    pg_stat_get_checkpointer_sync_time() AS sync_time,
    pg_stat_get_checkpointer_buffers_written() AS buffers_written,
    pg_stat_get_checkpointer_slru_written() AS slru_written,
    pg_stat_get_checkpointer_stat_reset_time() AS stats_reset;`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_checkpointer'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_checkpointer'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := checkpointerHeld(t, p, c, checkpointerSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, checkpointerSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation", err)
				}
			}
			const resetSQL = "SELECT pg_stat_reset_shared('checkpointer')"
			reset := checkpointerHeld(t, p, c, resetSQL)
			defer reset.Statement.Close()
			for _, executor := range []fixture.Executor{c, reset} {
				rows, err := executor.QueryContext(ctx, resetSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled reset returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary reset cancellation", err)
				}
			}
		}
		checkpointerSameReset(t, before, checkpointerGet(t, p, b, c))
	})
}

func checkpointerRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var expected [][]any
	for _, item := range []struct {
		oid, result int64
		name        string
	}{
		{2769, 20, "pg_stat_get_checkpointer_num_timed"},
		{2770, 20, "pg_stat_get_checkpointer_num_requested"},
		{2771, 20, "pg_stat_get_checkpointer_buffers_written"},
		{3160, 701, "pg_stat_get_checkpointer_write_time"},
		{3161, 701, "pg_stat_get_checkpointer_sync_time"},
		{6314, 1184, "pg_stat_get_checkpointer_stat_reset_time"},
		{6327, 20, "pg_stat_get_checkpointer_restartpoints_timed"},
		{6328, 20, "pg_stat_get_checkpointer_restartpoints_requested"},
		{6329, 20, "pg_stat_get_checkpointer_restartpoints_performed"},
		{6366, 20, "pg_stat_get_checkpointer_slru_written"},
		{6377, 20, "pg_stat_get_checkpointer_num_performed"},
	} {
		name := item.name
		expected = append(expected, []any{item.oid, name, int64(11), int64(12), float64(1), float64(0), "f", false, false, true, false, "s", "r", int64(0), int64(0), item.result, "", nil, nil, nil, name, nil, nil, nil})
	}
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid IN (2769,2770,6377,6327,6328,6329,3160,3161,2771,6366,6314) ORDER BY oid",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, expected)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}
