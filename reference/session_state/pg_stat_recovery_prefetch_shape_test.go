package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatRecoveryPrefetchShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := prefetchGet(t, p, b, c)
		for _, query := range []string{prefetchSQL, "SELECT * FROM pg_stat_recovery_prefetch", prefetchSQL + " WHERE FALSE"} {
			held := prefetchHeld(t, p, c, query)
			defer held.Statement.Close()
			// A freshly prepared statement and its retained reexecution must both
			// retain declared metadata, including for a zero-row result.
			for _, executor := range []fixture.Executor{c, held, held} {
				rows, err := executor.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if query == prefetchSQL + " WHERE FALSE" {
					prefetchMetadata(t, rows, b.PG)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty result", rows.Err())
					}
					rows.Close()
				} else {
					prefetchSame(t, before, prefetchRead(t, rows, b.PG))
				}
			}
		}
		var attributes [][]any
		for i, name := range prefetchNames {
			oid := int64(20)
			if i == 0 {
				oid = 1184
			} else if i >= 7 {
				oid = 23
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), int64(0), int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_recovery_prefetch'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_recovery_prefetch'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(10), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			identity := fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_recovery_prefetch'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12244), int64(12246), int64(12245)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, identity)
		}
		const definition = ` SELECT stats_reset,
    prefetch,
    hit,
    skip_init,
    skip_new,
    skip_fpw,
    skip_rep,
    wal_distance,
    block_distance,
    io_depth
   FROM pg_stat_get_recovery_prefetch() s(stats_reset, prefetch, hit, skip_init, skip_new, skip_fpw, skip_rep, wal_distance, block_distance, io_depth);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_recovery_prefetch'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_recovery_prefetch'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		// Synchronous pre-cancellation is a native adapter guarantee. pq can
		// admit a fast query despite a canceled context; see settingsTimeoutRecovery.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			held := prefetchHeld(t, p, c, prefetchSQL)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, prefetchSQL)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context cancellation", err)
				}
			}
		}
		prefetchSame(t, before, prefetchGet(t, p, b, c))
	})
}
