package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMAPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_numa_reader", "bc_numa_noinherit", "bc_numa_stats", "bc_numa_data", "bc_numa_scan", "bc_numa_super", "bc_numa_owner", "bc_numa_default"})
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_numa_reader", "CREATE ROLE bc_numa_noinherit NOINHERIT",
			"CREATE ROLE bc_numa_stats", "CREATE ROLE bc_numa_data", "CREATE ROLE bc_numa_scan", "CREATE ROLE bc_numa_super SUPERUSER", "CREATE ROLE bc_numa_owner", "CREATE ROLE bc_numa_default",
			"GRANT USAGE ON SCHEMA bc_part TO bc_numa_reader,bc_numa_noinherit,bc_numa_stats,bc_numa_data,bc_numa_scan,bc_numa_super,bc_numa_owner,bc_numa_default,pg_monitor",
			"GRANT pg_read_all_stats TO bc_numa_stats", "GRANT pg_read_all_data TO bc_numa_data", "GRANT pg_stat_scan_tables TO bc_numa_scan", "GRANT pg_monitor TO bc_numa_noinherit")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		capacity := bcCapacity(t, p, c)
		queries := []string{"SELECT count(DISTINCT bufferid) AS n FROM bc_part.pg_buffercache_numa", "SELECT count(DISTINCT bufferid) AS n FROM " + bcNUMAFunction()}
		held := make([]fixture.HeldStatement, len(queries))
		for i, q := range queries {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		allowed := func() {
			t.Helper()
			for i, q := range queries {
				s := bcNUMACount(q, capacity)
				bcNUMABoth(t, p, b, c, s)
				bcNUMADemand(t, p, b, held[i], s)
			}
			bcNUMAEmpty(t, p, b, c, "bc_part.pg_buffercache_numa", true)
			bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
		}
		denied := func(viewMessage string) {
			t.Helper()
			for i, q := range queries {
				message := "permission denied for function pg_buffercache_numa_pages"
				if i == 0 {
					message = viewMessage
				}
				s := bcError(q, "42501", message)
				bcFailure(t, p, b, c, s, "execute")
				bcRun(t, p, b, held[i], s)
			}
			// ACL precedes initialization and successful-model shape validation.
			bad := "SELECT * FROM " + bcFrom("bc_part.pg_buffercache_numa_pages", bcNUMANames[:2], bcNUMASQLTypes[:2])
			bcFailure(t, p, b, c, bcError(bad, "42501", "permission denied for function pg_buffercache_numa_pages"), "execute")
		}
		allowed()
		for _, role := range []string{"bc_numa_reader", "bc_numa_noinherit", "bc_numa_stats", "bc_numa_data", "bc_numa_scan"} {
			bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
			message := "permission denied for view pg_buffercache_numa"
			if role == "bc_numa_data" {
				// pg_read_all_data supplies relation SELECT, not function EXECUTE.
				message = "permission denied for function pg_buffercache_numa_pages"
			}
			denied(message)
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		}
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_numa_super")
		allowed()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION bc_numa_noinherit", "SET ROLE pg_monitor")
		allowed()
		bcDDL(t, p, b, c, "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT pg_monitor TO bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		allowed()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE pg_monitor FROM bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		denied("permission denied for view pg_buffercache_numa")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		denied("permission denied for function pg_buffercache_numa_pages")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON bc_part.pg_buffercache_numa FROM bc_numa_reader", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		bcFailure(t, p, b, c, bcError(queries[0], "42501", "permission denied for view pg_buffercache_numa"), "execute")
		bcRun(t, p, b, held[0], bcError(queries[0], "42501", "permission denied for view pg_buffercache_numa"))
		bcNUMABoth(t, p, b, c, bcNUMACount(queries[1], capacity))
		bcNUMADemand(t, p, b, held[1], bcNUMACount(queries[1], capacity))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		allowed()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() FROM bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		denied("permission denied for function pg_buffercache_numa_pages")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO bc_numa_reader", "REVOKE SELECT ON bc_part.pg_buffercache_numa FROM bc_numa_reader", "SET SESSION AUTHORIZATION bc_numa_reader")
		bcRun(t, p, b, held[0], bcError(queries[0], "42501", "permission denied for view pg_buffercache_numa"))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_reader")
		bcNUMASchemaUsage(t, p, b, c, queries, capacity)
		bcDDL(t, p, b, c, "GRANT SELECT ON bc_part.pg_buffercache_numa TO PUBLIC", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO PUBLIC", "SET SESSION AUTHORIZATION bc_numa_stats")
		allowed()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON bc_part.pg_buffercache_numa FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() FROM PUBLIC", "SET SESSION AUTHORIZATION bc_numa_stats")
		denied("permission denied for view pg_buffercache_numa")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")

		t.Run("ordinary_wrapper_authority", func(t *testing.T) {
			bcDDL(t, p, b, c,
				"GRANT USAGE,CREATE ON SCHEMA bc_part TO bc_numa_owner",
				"GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_owner",
				"CREATE VIEW bc_part.numa_definer AS SELECT * FROM bc_part.pg_buffercache_numa",
				"ALTER VIEW bc_part.numa_definer OWNER TO bc_numa_owner",
				"CREATE VIEW bc_part.numa_invoker WITH (security_invoker=true) AS SELECT * FROM bc_part.pg_buffercache_numa",
				"ALTER VIEW bc_part.numa_invoker OWNER TO bc_numa_owner",
				"GRANT SELECT ON bc_part.numa_definer,bc_part.numa_invoker TO bc_numa_stats",
				"SET SESSION AUTHORIZATION bc_numa_stats")
			definer := "SELECT count(DISTINCT bufferid) AS n FROM bc_part.numa_definer"
			invoker := "SELECT count(DISTINCT bufferid) AS n FROM bc_part.numa_invoker"
			bcFailure(t, p, b, c, bcError(definer, "42501", "permission denied for function pg_buffercache_numa_pages"), "execute")
			bcFailure(t, p, b, c, bcError(invoker, "42501", "permission denied for view pg_buffercache_numa"), "execute")
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() TO bc_numa_stats", "SET SESSION AUTHORIZATION bc_numa_stats")
			bcNUMABoth(t, p, b, c, bcNUMACount(definer, capacity))
			bcFailure(t, p, b, c, bcError(invoker, "42501", "permission denied for view pg_buffercache_numa"), "execute")
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache_numa TO bc_numa_stats", "SET SESSION AUTHORIZATION bc_numa_stats")
			bcNUMABoth(t, p, b, c, bcNUMACount(invoker, capacity))
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		})
		// Named creation defaults survive the script's PUBLIC revoke. Their
		// installation snapshot is distinct from subsequent live ACL changes.
		bcDDL(t, p, b, c, "DROP EXTENSION pg_buffercache CASCADE",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT EXECUTE ON FUNCTIONS TO bc_numa_default",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT SELECT ON TABLES TO bc_numa_default",
			"CREATE EXTENSION pg_buffercache SCHEMA bc_part", "SET SESSION AUTHORIZATION bc_numa_default")
		for _, q := range queries {
			bcNUMABoth(t, p, b, c, bcNUMACount(q, capacity))
		}
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.oid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs=p.proacl",
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND c.oid='bc_part.pg_buffercache_numa'::regclass AND i.privtype='e' AND i.objsubid=0 AND i.initprivs=c.relacl",
		} {
			bcBoth(t, p, b, c, bcNUMACount(q, 1))
		}
		bcDDL(t, p, b, c, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_numa_pages() FROM bc_numa_default", "SET SESSION AUTHORIZATION bc_numa_default")
		bcFailure(t, p, b, c, bcError(queries[1], "42501", "permission denied for function pg_buffercache_numa_pages"), "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.oid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>p.proacl", 1))
	})
}

func bcNUMASchemaUsage(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn, queries []string, capacity int64) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_numa_reader")
	held := make([]fixture.HeldStatement, len(queries))
	for i, q := range queries {
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		bcNUMADemand(t, p, b, held[i], bcNUMACount(q, capacity))
	}
	bcDDL(t, p, b, admin, "REVOKE USAGE ON SCHEMA bc_part FROM bc_numa_reader")
	for i, q := range queries {
		s := bcError(q, "42501", "permission denied for schema bc_part")
		s.Diagnostic.Position = fixture.Position(strings.Index(q, "bc_part") + 1)
		s.ErrorStage = "execute"
		bcRun(t, p, b, held[i], s)
		bcFailure(t, p, b, c, s, "prepare")
	}
	bcDDL(t, p, b, admin, "GRANT USAGE ON SCHEMA bc_part TO bc_numa_reader")
	for i, q := range queries {
		bcNUMABoth(t, p, b, c, bcNUMACount(q, capacity))
		bcNUMADemand(t, p, b, held[i], bcNUMACount(q, capacity))
	}
}
