package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSLRUShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := slruGet(t, p, b, c)
		for _, query := range []string{slruSQL, "SELECT * FROM pg_stat_slru", "SELECT * FROM pg_catalog.pg_stat_slru WHERE FALSE"} {
			held := slruHeld(t, p, c, query)
			defer held.Statement.Close()
			// A freshly prepared statement and its retained reexecution must both
			// retain declared metadata, including for a zero-row result.
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == "SELECT * FROM pg_catalog.pg_stat_slru WHERE FALSE" {
					slruMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					slruSame(t, before, slruRead(t, rows, b.PG))
				}
			}
		}
		for _, query := range []string{"SELECT name FROM pg_stat_slru ORDER BY name", "SELECT name FROM pg_catalog.pg_stat_slru ORDER BY name"} {
			var expected [][]any
			for _, name := range slruCategories { expected = append(expected, []any{name}) }
			step := fixture.Select(query, []string{"name"}, []string{"TEXT"}, expected)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		step := fixture.Select("SELECT name,blks_hit,truncates FROM pg_stat_slru WHERE name=$1", []string{"name","blks_hit","truncates"}, []string{"TEXT","INT8","INT8"}, [][]any{{"other", int64(0), int64(0)}}, "other")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		var attributes [][]any
		for i, name := range slruNames {
			oid := int64(20)
			collation := int64(0)
			if i == 0 { oid, collation = 25, 100 }
			if i == 8 { oid = 1184 }
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_slru'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_slru'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(9), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			identity := fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_slru'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12236), int64(12238), int64(12237)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, identity)
		}
		const definition = ` SELECT name,
    blks_zeroed,
    blks_hit,
    blks_read,
    blks_written,
    blks_exists,
    flushes,
    truncates,
    stats_reset
   FROM pg_stat_get_slru() s(name, blks_zeroed, blks_hit, blks_read, blks_written, blks_exists, flushes, truncates, stats_reset);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_slru'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_slru'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		// Synchronous pre-cancellation is a native adapter guarantee. pq can
		// admit a fast query despite a canceled context; see settingsTimeoutRecovery.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := slruHeld(t, p, c, slruSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, slruSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context cancellation", err)
				}
			}
		}
		slruSame(t, before, slruGet(t, p, b, c))
	})
}
