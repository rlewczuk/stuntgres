package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMAShape(t *testing.T) {
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		for _, tc := range []struct {
			name, from string
			names []string
			view bool
		}{
			{"view", "bc_part.pg_buffercache_numa", bcNUMANames, true},
			{"function", bcNUMAFunction(), bcNUMANames, false},
			{"renamed", bcFrom("bc_part.pg_buffercache_numa_pages", []string{"id", "page", "node"}, bcNUMASQLTypes), []string{"id", "page", "node"}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, empty := range []bool{true, false} {
					q := "SELECT * FROM " + tc.from
					if empty {
						q += " WHERE false"
					}
					st, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
					for i, source := range []fixture.Executor{c, held, held} {
						t.Run(fmt.Sprintf("empty_%v/route_%d", empty, i), func(t *testing.T) {
							if !empty && b.PG && bcNUMAPGUnavailable {
								bcNUMADemand(t, p, b, source, fixture.Select(q, tc.names, bcNUMATypes, nil))
								return
							}
							rows, err := source.QueryContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							got := bcRows(t, rows, b.PG, tc.view, tc.names, bcNUMATypes)
							if empty {
								if len(got) != 0 {
									t.Fatal("undemanded NUMA emitted rows", got)
								}
							} else {
								bcNUMATopology(t, got, b.PG, capacity)
							}
						})
					}
					// A separately prepared execution is not replaced by held reuse.
					if empty {
						s := fixture.Select(q, tc.names, bcNUMATypes, nil)
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { bcMetadata(t, rows, b.PG, tc.view, tc.names, bcNUMATypes) }
						bcRun(t, p, b, c, fixture.Prepared(s))
					} else {
						fresh, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						if b.PG && bcNUMAPGUnavailable {
							bcNUMADemand(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: q}, fixture.Select(q, tc.names, bcNUMATypes, nil))
						} else {
							rows, err := fresh.QueryContext(p.Ctx)
							if err != nil {
								fresh.Close()
								t.Fatal(err)
							}
							bcNUMATopology(t, bcRows(t, rows, b.PG, tc.view, tc.names, bcNUMATypes), b.PG, capacity)
						}
						if err := fresh.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
		bcNUMAEmpty(t, p, b, c, "bc_part.pg_buffercache_numa", true)
		bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
		bcBoth(t, p, b, c, fixture.Select("SELECT attnum::integer AS n,attname::text AS name,atttypid::bigint AS type,attnotnull FROM pg_attribute WHERE attrelid='bc_part.pg_buffercache_numa'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"n", "name", "type", "attnotnull"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, [][]any{{int64(1), "bufferid", int64(23), false}, {int64(2), "os_page_num", int64(20), false}, {int64(3), "numa_node", int64(23), false}}))
		q := "SELECT p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proretset,p.provolatile::text AS volatility,p.proisstrict,p.proparallel::text AS parallel,p.prosecdef,p.proargnames::text AS names,p.proallargtypes::text AS outputs,l.lanname::text AS language,p.prosrc,p.probin,p.procost::bigint AS cost,p.prorows::bigint AS rows,pg_get_userbyid(p.proowner)=current_user AS owner FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='bc_part.pg_buffercache_numa_pages()'::regprocedure"
		bcBoth(t, p, b, c, fixture.Select(q, []string{"nargs", "result", "proretset", "volatility", "proisstrict", "parallel", "prosecdef", "names", "outputs", "language", "prosrc", "probin", "cost", "rows", "owner"}, []string{"INT4", "INT8", "BOOL", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "INT8", "BOOL"}, [][]any{{int64(0), int64(2249), true, "v", false, "s", false, nil, nil, "c", "pg_buffercache_numa_pages", "$libdir/pg_buffercache", int64(1), int64(1000), true}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,pg_get_userbyid(extowner)=current_user AS owner FROM pg_extension WHERE extname='pg_buffercache'", []string{"extversion", "extrelocatable", "owner"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"1.6", true, true}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_buffercache' AND version='1.6'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.6", true, false, true}}))
		ids := bcNUMACatalogLinks(t, p, b, c, "bc_part.pg_buffercache_numa_pages()", "bc_part.pg_buffercache_numa")
		bcNUMAMembership(t, p, b, c, ids, 2)
		for _, tc := range []struct {
			class int
			object, ownerACL, monitor string
		}{
			{1255, "'bc_part.pg_buffercache_numa_pages()'::regprocedure", "=X/", "pg_monitor=X/"},
			{1259, "'bc_part.pg_buffercache_numa'::regclass", "=arwdDxtm/", "pg_monitor=r/"},
		} {
			q := "SELECT initprivs=ARRAY[(current_user::text||$1::text||current_user::text)::aclitem,($2::text||current_user::text)::aclitem] AS initial FROM pg_init_privs WHERE classoid=$3::oid AND objoid=" + tc.object + " AND objsubid=0 AND privtype='e'"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"initial"}, []string{"BOOL"}, [][]any{{true}}, tc.ownerACL, tc.monitor, tc.class))
		}
		for _, q := range []string{
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND i.objoid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND i.objsubid=0 AND i.privtype='e' AND i.initprivs=p.proacl",
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND i.objoid='bc_part.pg_buffercache_numa'::regclass AND i.objsubid=0 AND i.privtype='e' AND i.initprivs=c.relacl",
			"SELECT count(*) AS n FROM pg_rewrite r JOIN pg_depend d ON d.classid=2618 AND d.objid=r.oid WHERE r.ev_class='bc_part.pg_buffercache_numa'::regclass AND d.refclassid=1255 AND d.refobjid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND d.deptype='n'",
		} {
			bcBoth(t, p, b, c, bcNUMACount(q, 1))
		}
		if !b.PG {
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts','pg_buffercache_numa_pages')", 7))
		}
	})
}
