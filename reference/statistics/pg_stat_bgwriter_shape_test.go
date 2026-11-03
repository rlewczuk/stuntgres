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

func TestReferencePGStatBGWriterShape(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := bgwriterGet(t, p, b, c)
		for _, query := range []string{bgwriterSQL, "SELECT * FROM pg_stat_bgwriter", bgwriterSQL + " WHERE FALSE"} {
			held := bgwriterHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == bgwriterSQL+" WHERE FALSE" {
					bgwriterMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					bgwriterSameReset(t, before, bgwriterRead(t, rows, b.PG))
				}
			}
		}
		for _, query := range []string{"SELECT stats_reset,buffers_alloc,buffers_clean,buffers_clean,maxwritten_clean FROM pg_stat_bgwriter", "SELECT stats_reset,buffers_alloc,buffers_clean,buffers_clean,maxwritten_clean FROM pg_catalog.pg_stat_bgwriter"} {
			selected := bgwriterHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected, selected} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				names, err := rows.Columns()
				want := []string{"stats_reset", "buffers_alloc", "buffers_clean", "buffers_clean", "maxwritten_clean"}
				if err != nil || !reflect.DeepEqual(names, want) {
					t.Fatal("reordered/repeated names", names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 5 {
					t.Fatal("reordered descriptors", types, err)
				}
				for i, typ := range types {
					expected := "INT8"
					if i == 0 {
						expected = "TIMESTAMPTZ"
					}
					if typ.DatabaseTypeName() != expected {
						t.Fatal("reordered type", i, typ.DatabaseTypeName())
					}
				}
				if !rows.Next() {
					t.Fatal("missing reordered singleton", rows.Err())
				}
				var at time.Time
				var allocated, clean, repeated, limited int64
				if err := rows.Scan(&at, &allocated, &clean, &repeated, &limited); err != nil || !at.Equal(before.reset) || clean != repeated || allocated < 0 || clean < 0 || limited < 0 {
					t.Fatal("repeated values and reset", at, allocated, clean, repeated, limited, err)
				}
				if !b.PG && (allocated != 0 || clean != 0 || limited != 0) {
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
		for i, name := range bgwriterNames {
			oid, collation := int64(1184), int64(0)
			if i < 3 {
				oid = 20
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_bgwriter'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_bgwriter'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(4), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		bgwriterRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_bgwriter'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12293), int64(12295), int64(12294)}}))
		}
		const definition = ` SELECT pg_stat_get_bgwriter_buf_written_clean() AS buffers_clean,
    pg_stat_get_bgwriter_maxwritten_clean() AS maxwritten_clean,
    pg_stat_get_buf_alloc() AS buffers_alloc,
    pg_stat_get_bgwriter_stat_reset_time() AS stats_reset;`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_bgwriter'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_bgwriter'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := bgwriterHeld(t, p, c, bgwriterSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, bgwriterSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation", err)
				}
			}
		}
		bgwriterSameReset(t, before, bgwriterGet(t, p, b, c))
	})
}

func bgwriterRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var expected [][]any
	for i, name := range []string{"pg_stat_get_bgwriter_buf_written_clean", "pg_stat_get_bgwriter_maxwritten_clean", "pg_stat_get_buf_alloc", "pg_stat_get_bgwriter_stat_reset_time"} {
		result := int64(20)
		if i == 3 {
			result = 1184
		}
		expected = append(expected, []any{([]int64{2772, 2773, 2859, 3075})[i], name, int64(11), int64(12), float64(1), float64(0), "f", false, false, true, false, "s", "r", int64(0), int64(0), result, "", nil, nil, nil, name, nil, nil, nil})
	}
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid IN (2772,2773,2859,3075) ORDER BY oid",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, expected)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}
