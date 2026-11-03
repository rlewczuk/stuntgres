package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercachePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_reader", "bc_noinherit", "bc_stats", "bc_scan", "bc_super", "bc_boundary_owner", "bc_boundary_reader"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_reader", "CREATE ROLE bc_noinherit NOINHERIT",
			"CREATE ROLE bc_stats", "CREATE ROLE bc_scan", "CREATE ROLE bc_super SUPERUSER",
			"GRANT USAGE ON SCHEMA bc_part TO bc_reader,bc_noinherit,bc_stats,bc_scan,bc_super,pg_monitor",
			"GRANT pg_read_all_stats TO bc_stats", "GRANT pg_stat_scan_tables TO bc_scan",
			"GRANT pg_monitor TO bc_noinherit", "CREATE TABLE bc_part.secret_heap(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.secret_heap VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.secret_heap")
		capacity := bcCapacity(t, p, c)
		view := "SELECT count(*) AS n FROM bc_part.pg_buffercache"
		function := "SELECT count(*) AS n FROM " + bcFunction()
		queries := []string{view, function}
		messages := []string{"permission denied for view pg_buffercache", "permission denied for function pg_buffercache_pages"}
		var held []fixture.HeldStatement
		for _, q := range queries {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held = append(held, fixture.HeldStatement{Executor: c, Statement: st, SQL: q})
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		allowed := func(source fixture.Executor) {
			t.Helper()
			for _, q := range queries {
				bcRun(t, p, b, source, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
			}
		}
		denied := func() {
			t.Helper()
			for i, q := range queries {
				s := bcError(q, "42501", messages[i])
				bcFailure(t, p, b, c, s, "execute")
				bcRun(t, p, b, held[i], s)
			}
			wrong := "SELECT * FROM " + bcFrom("bc_part.pg_buffercache_pages", bcNames[:7], bcSQLTypes[:7])
			bcFailure(t, p, b, c, bcError(wrong, "42501", messages[1]), "execute")
		}
		allowed(c)
		for _, role := range []string{"bc_reader", "bc_noinherit", "bc_stats", "bc_scan"} {
			bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
			denied()
			bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		}
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_super")
		allowed(c)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION bc_noinherit", "SET ROLE pg_monitor")
		allowed(c)
		bcDDL(t, p, b, c, "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT pg_monitor TO bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		allowed(c)
		for _, h := range held {
			allowed(h)
		}
		bcFailure(t, p, b, c, bcError("SELECT k FROM bc_part.secret_heap", "42501", "permission denied for table secret_heap"), "execute")
		bcUsage(t, p, b, c, "bc_part.secret_heap", 1)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE pg_monitor FROM bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		denied()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		// SELECT alone reaches the invoker's missing EXECUTE, even via the view.
		for _, q := range queries {
			bcFailure(t, p, b, c, bcError(q, "42501", messages[1]), "execute")
		}
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON bc_part.pg_buffercache FROM bc_reader", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		bcFailure(t, p, b, c, bcError(view, "42501", messages[0]), "execute")
		bcBoth(t, p, b, c, fixture.Select(function, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		allowed(c)
		for _, h := range held {
			allowed(h)
		}
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() FROM bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		for i, q := range queries {
			bcRun(t, p, b, held[i], bcError(q, "42501", messages[1]))
		}
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_reader", "REVOKE SELECT ON bc_part.pg_buffercache FROM bc_reader", "SET SESSION AUTHORIZATION bc_reader")
		bcRun(t, p, b, held[0], bcError(view, "42501", messages[0]))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO bc_reader")
		bcSchemaUsage(t, p, b, c, queries, capacity)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO PUBLIC", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO PUBLIC", "SET SESSION AUTHORIZATION bc_stats")
		allowed(c)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON bc_part.pg_buffercache FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() FROM PUBLIC", "SET SESSION AUTHORIZATION bc_stats")
		denied()
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		t.Run("view_boundary_authority", func(t *testing.T) {
			bcViewBoundary(t, p, b, c, capacity)
		})
	})
}

func bcViewBoundary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, capacity int64) {
	t.Helper()
	bcDDL(t, p, b, c,
		"CREATE ROLE bc_boundary_owner", "CREATE ROLE bc_boundary_reader",
		"GRANT USAGE,CREATE ON SCHEMA bc_part TO bc_boundary_owner",
		"GRANT USAGE ON SCHEMA bc_part TO bc_boundary_reader",
		"GRANT SELECT(bufferid) ON bc_part.pg_buffercache TO bc_boundary_owner",
		"CREATE VIEW bc_part.bc_boundary_inner AS SELECT bufferid FROM bc_part.pg_buffercache",
		"ALTER VIEW bc_part.bc_boundary_inner OWNER TO bc_boundary_owner",
		"CREATE VIEW bc_part.bc_boundary_outer AS SELECT bufferid FROM bc_part.bc_boundary_inner",
		"ALTER VIEW bc_part.bc_boundary_outer OWNER TO bc_boundary_owner",
		"CREATE VIEW bc_part.bc_boundary_outer_invoker WITH (security_invoker=true) AS SELECT bufferid FROM bc_part.bc_boundary_inner",
		"ALTER VIEW bc_part.bc_boundary_outer_invoker OWNER TO bc_boundary_owner",
		"CREATE VIEW bc_part.bc_boundary_inner_invoker WITH (security_invoker=true) AS SELECT bufferid FROM bc_part.pg_buffercache",
		"ALTER VIEW bc_part.bc_boundary_inner_invoker OWNER TO bc_boundary_owner",
		"CREATE VIEW bc_part.bc_boundary_chain AS SELECT bufferid FROM bc_part.bc_boundary_inner_invoker",
		"ALTER VIEW bc_part.bc_boundary_chain OWNER TO bc_boundary_owner",
		"GRANT SELECT(bufferid) ON bc_part.bc_boundary_outer,bc_part.bc_boundary_outer_invoker,bc_part.bc_boundary_chain TO bc_boundary_reader")
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	query := func(name string) string { return "SELECT bufferid FROM bc_part." + name + " ORDER BY bufferid LIMIT 1" }
	denied := func(q, object string) {
		t.Helper()
		bcFailure(t, p, b, c, bcError(q, "42501", "permission denied for "+object), "execute")
	}
	allowed := func(name string) {
		t.Helper()
		bcBoth(t, p, b, c, fixture.Select(query(name), []string{"bufferid"}, []string{"INT4"}, [][]any{{int64(1)}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part."+name, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
	}
	bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_boundary_reader")
	// Outer SELECT is admitted, but a definer relation owner cannot lend its
	// invoker function privilege to the reader.
	denied(query("bc_boundary_outer"), "function pg_buffercache_pages")
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_boundary_reader", "SET SESSION AUTHORIZATION bc_boundary_reader")
	allowed("bc_boundary_outer")
	// A security_invoker layer changes relation access back to the original
	// invoker, even when it is reached below a definer-owned outer view.
	denied(query("bc_boundary_outer_invoker"), "view bc_boundary_inner")
	denied(query("bc_boundary_chain"), "view pg_buffercache")
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION",
		"GRANT SELECT(bufferid) ON bc_part.bc_boundary_inner,bc_part.pg_buffercache TO bc_boundary_reader",
		"SET SESSION AUTHORIZATION bc_boundary_reader")
	allowed("bc_boundary_outer_invoker")
	allowed("bc_boundary_chain")
	allowed("pg_buffercache")
	// One column authorizes its own projection and count(*), not the full
	// nine-field projection. Both denied must report the outer view first.
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() FROM bc_boundary_reader", "SET SESSION AUTHORIZATION bc_boundary_reader")
	full := "SELECT * FROM bc_part.pg_buffercache"
	denied(full, "view pg_buffercache")
	denied(query("pg_buffercache"), "function pg_buffercache_pages")
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON bc_part.pg_buffercache TO bc_boundary_reader", "SET SESSION AUTHORIZATION bc_boundary_reader")
	denied(full, "function pg_buffercache_pages")
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON bc_part.bc_boundary_outer FROM bc_boundary_reader", "REVOKE SELECT(bufferid) ON bc_part.bc_boundary_outer FROM bc_boundary_reader", "SET SESSION AUTHORIZATION bc_boundary_reader")
	denied(query("bc_boundary_outer"), "view bc_boundary_outer")
	bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
}

func bcSchemaUsage(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn, queries []string, capacity int64) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_reader")
	held := make([]fixture.HeldStatement, len(queries))
	for i, q := range queries {
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		bcRun(t, p, b, held[i], fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
	}
	// No role, membership or object ACL changes occur on the prepared owner.
	// Only a separate administrator revokes its namespace lookup privilege.
	bcDDL(t, p, b, admin, "REVOKE USAGE ON SCHEMA bc_part FROM bc_reader")
	t.Run("schema_usage_held_resolution", func(t *testing.T) {
		for i, q := range queries {
			t.Run([]string{"view", "function"}[i], func(t *testing.T) {
				// Pinned 18.6 rejects the held view after namespace ACL
				// invalidation. Reanalysis must not retain revoked lookup rights.
				s := bcError(q, "42501", "permission denied for schema bc_part")
				s.Diagnostic.Position = fixture.Position(strings.Index(q, "bc_part") + 1)
				s.ErrorStage = "execute"
				bcRun(t, p, b, held[i], s)
			})
		}
	})
	t.Run("schema_usage_fresh_lookup", func(t *testing.T) {
		for _, q := range queries {
			s := bcError(q, "42501", "permission denied for schema bc_part")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "bc_part") + 1)
			bcFailure(t, p, b, c, s, "prepare")
		}
	})
	bcDDL(t, p, b, admin, "GRANT USAGE ON SCHEMA bc_part TO bc_reader")
	for i, q := range queries {
		s := fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{capacity}})
		bcBoth(t, p, b, c, s)
		bcRun(t, p, b, held[i], s)
	}
}
