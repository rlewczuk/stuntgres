package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatArchiverShape(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := archiverGet(t, p, b, c)
		for _, query := range []string{archiverSQL, "SELECT * FROM pg_stat_archiver", archiverSQL + " WHERE FALSE"} {
			held := archiverHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == archiverSQL+" WHERE FALSE" {
					archiverMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					archiverSame(t, before, archiverRead(t, rows, b.PG))
				}
			}
		}
		var attributes [][]any
		for i, name := range archiverNames {
			oid, collation := int64(1184), int64(0)
			if i == 0 || i == 3 {
				oid = 20
			} else if i == 1 || i == 4 {
				oid, collation = 25, 100
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_archiver'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_archiver'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(7), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		archiverRoutineMetadata(t, p, b, c)
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_archiver'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12289), int64(12291), int64(12290)}}))
		}
		const definition = ` SELECT archived_count,
    last_archived_wal,
    last_archived_time,
    failed_count,
    last_failed_wal,
    last_failed_time,
    stats_reset
   FROM pg_stat_get_archiver() s(archived_count, last_archived_wal, last_archived_time, failed_count, last_failed_wal, last_failed_time, stats_reset);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_archiver'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_archiver'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := archiverHeld(t, p, c, archiverSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, archiverSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation", err)
				}
			}
		}
		archiverSame(t, before, archiverGet(t, p, b, c))
	})
}

func archiverRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	step := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,prolang::bigint,procost,prorows,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prosrc,probin,prosqlbody::text,proconfig::text FROM pg_proc WHERE oid=3195",
		[]string{"oid", "proname", "pronamespace", "prolang", "procost", "prorows", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prosrc", "probin", "prosqlbody", "proconfig"},
		[]string{"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(3195), "pg_stat_get_archiver", int64(11), int64(12), float64(1), float64(0), "f", false, false, false, false, "s", "r", int64(0), int64(0), int64(2249), "", "{20,25,1184,20,25,1184,1184}", "{o,o,o,o,o,o,o}", "{archived_count,last_archived_wal,last_archived_time,failed_count,last_failed_wal,last_failed_time,stats_reset}", "pg_stat_get_archiver", nil, nil, nil}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}
