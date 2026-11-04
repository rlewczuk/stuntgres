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

func TestReferencePGStatWALShape(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := walGet(t, p, b, c)
		for _, query := range []string{walSQL, "SELECT * FROM pg_stat_wal", walSQL + " WHERE FALSE"} {
			held := archiverHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == walSQL+" WHERE FALSE" {
					walMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					walSame(t, before, walRead(t, rows, b.PG))
				}
			}
		}
		walReordered(t, p, b, c, before)
		walRawValues(t, p, b, c)
		var attributes [][]any
		for i, name := range walNames {
			oid, collation := int64(1184), int64(0)
			if i == 0 || i == 1 || i == 3 {
				oid = 20
			} else if i == 2 {
				oid = 1700
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_wal'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_wal'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(5), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		// Bootstrap-pinned getter1136 need not have a pg_depend edge. The
		// rewrite still has its ordinary internal ownership of the real view.
		dependency := fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='pg_catalog.pg_stat_wal'::regclass AND r.rulename='_RETURN' AND d.refclassid='pg_class'::regclass AND d.refobjid='pg_catalog.pg_stat_wal'::regclass AND d.deptype='i'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, dependency)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(dependency))
		rewrite := fixture.Select("SELECT ev_type='1' AS select_rule,is_instead FROM pg_rewrite WHERE ev_class='pg_catalog.pg_stat_wal'::regclass AND rulename='_RETURN'", []string{"select_rule", "is_instead"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, rewrite)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(rewrite))
		walRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_wal'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12305), int64(12307), int64(12306)}}))
		}
		const definition = ` SELECT wal_records,
    wal_fpi,
    wal_bytes,
    wal_buffers_full,
    stats_reset
   FROM pg_stat_get_wal() w(wal_records, wal_fpi, wal_bytes, wal_buffers_full, stats_reset);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_wal'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_wal'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := archiverHeld(t, p, c, walSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, walSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation", err)
				}
			}
		}
		walSame(t, before, walGet(t, p, b, c))
	})
}

func walRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid=1136",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(1136), "pg_stat_get_wal", int64(11), int64(12), float64(1), float64(0), "f", false, false, false, false, "s", "r", int64(0), int64(0), int64(2249), "", "{20,20,1700,20,1184}", "{o,o,o,o,o}", "{wal_records,wal_fpi,wal_bytes,wal_buffers_full,stats_reset}", "pg_stat_get_wal", nil, nil, nil}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}

func walReordered(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, before walRow) {
	t.Helper()
	const query = "SELECT stats_reset,wal_bytes,wal_records,wal_records,wal_fpi,wal_buffers_full FROM pg_catalog.pg_stat_wal"
	held := archiverHeld(t, p, c, query)
	defer held.Statement.Close()
	for _, executor := range []fixture.Executor{c, held} {
		rows, err := executor.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		want := []string{"stats_reset", "wal_bytes", "wal_records", "wal_records", "wal_fpi", "wal_buffers_full"}
		if err != nil || !reflect.DeepEqual(names, want) {
			t.Fatal("reordered/repeated column names", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 6 {
			t.Fatal("six reordered descriptors", types, err)
		}
		for i, typ := range types {
			if typ.DatabaseTypeName() != []string{"TIMESTAMPTZ", "NUMERIC", "INT8", "INT8", "INT8", "INT8"}[i] {
				t.Fatal("reordered type", i, typ.DatabaseTypeName())
			}
		}
		if !rows.Next() {
			t.Fatal("missing reordered singleton", rows.Err())
		}
		var at time.Time
		var bytes string
		var records, repeated, fpi, full int64
		if err := rows.Scan(&at, &bytes, &records, &repeated, &fpi, &full); err != nil || records != repeated || !at.Equal(before.reset) || records < 0 || fpi < 0 || full < 0 {
			t.Fatal("reordered values and repeated occurrence", at, bytes, records, repeated, fpi, full, err)
		}
		if !b.PG && (records != 0 || fpi != 0 || full != 0 || bytes != "0") {
			t.Fatal("fabricated physical WAL")
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra reordered row", rows.Err())
		}
		rows.Close()
	}
}

func walRawValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	held := archiverHeld(t, p, c, walSQL)
	defer held.Statement.Close()
	for _, executor := range []fixture.Executor{c, held} {
		rows, err := executor.QueryContext(p.Ctx, walSQL)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing raw singleton", rows.Err())
		}
		var values [5]any
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4]); err != nil {
			t.Fatal(err)
		}
		for _, column := range []int{0, 1, 3} {
			if _, ok := values[column].(int64); !ok {
				t.Fatal("raw INT8", column, values[column])
			}
		}
		if _, ok := values[4].(time.Time); !ok {
			t.Fatal("raw TIMESTAMPTZ", values[4])
		}
		if !b.PG {
			if value, ok := values[2].(string); !ok || value != "0" {
				t.Fatal("native exact raw NUMERIC string", values[2])
			}
		} else if value, ok := values[2].([]byte); !ok || len(value) == 0 {
			t.Fatal("pq raw NUMERIC bytes", values[2])
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra raw singleton", rows.Err())
		}
		rows.Close()
	}
}
