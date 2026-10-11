package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedRebindPath(t *testing.T) {
	preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		preparedRebindSetup(t, p, b, c)
		routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_path AS SELECT v FROM t", "EXECUTE rb_path")
		read := func(value int64) {
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{value}})
			}
		}
		read(7)
		preparedRebindShadow(t, p, b, c)
		raw := "rb_early, rb_late, rb_late, rb_missing"
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT set_config('search_path','"+raw+"',false)", "set_config", "TEXT", raw))
		read(7) // Different text, equal resolved namespace identities.
		sqlPreparedStep(t, p, b, c, fixture.Exec("SET search_path TO rb_late,rb_early", 0))
		read(7) // Different effective path, same first source.
		sqlPreparedStep(t, p, b, c, fixture.Exec("SET search_path TO rb_early,rb_late", 0))
		read(9)
		// A separate physical session's path and held definition stay private.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		sqlPreparedStep(t, p, b, peer, fixture.Exec("SET search_path TO rb_late,rb_early", 0))
		peerHeld := preparedRebindRoute{Executor: preparedRebindHold(t, p, peer, "SELECT v FROM t"), SQL: "SELECT v FROM t"}
		preparedRebindRead(t, p, b, peerHeld, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		sqlPreparedStep(t, p, b, c, fixture.Exec("SET search_path TO rb_late,rb_early", 0))
		read(7)
		preparedRebindRead(t, p, b, peerHeld, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func TestReferencePreparedRebindNamespace(t *testing.T) {
	for _, action := range []string{"create", "rename", "drop"} {
		t.Run("callback_"+action, func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				preparedRebindSetup(t, p, b, c)
				if action != "create" {
					sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE SCHEMA rb_other", 0))
				}
				routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_namespace AS SELECT v FROM t", "EXECUTE rb_namespace")
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				}
				preparedRebindShadow(t, p, b, c)
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				}
				query := "CREATE SCHEMA rb_other"
				if action == "rename" {
					query = "ALTER SCHEMA rb_other RENAME TO rb_renamed"
				} else if action == "drop" {
					query = "DROP SCHEMA rb_other"
				}
				sqlPreparedStep(t, p, b, c, fixture.Exec(query, 0))
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
				}
			})
		})
	}

	t.Run("missing_schema", func(t *testing.T) {
		preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			preparedRebindSetup(t, p, b, c)
			sqlPreparedStep(t, p, b, c, fixture.Exec("SET search_path TO rb_missing,rb_late", 0))
			routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_missing_path AS SELECT v FROM t", "EXECUTE rb_missing_path")
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			for _, q := range []string{"CREATE SCHEMA rb_missing", "CREATE TABLE rb_missing.t(v INT)"} {
				sqlPreparedStep(t, p, b, c, fixture.Exec(q, 0))
			}
			sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO rb_missing.t VALUES(9)", 1))
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			}
		})
	})

	t.Run("temp_introduction", func(t *testing.T) {
		preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			preparedRebindSetup(t, p, b, c)
			routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_temp_path AS SELECT v FROM t", "EXECUTE rb_temp_path")
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TEMP TABLE t(v INT)", 0))
			sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(9)", 1))
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			}
		})
	})

	t.Run("usage_and_table_acl", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rb_reader"})
		preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			preparedRebindSetup(t, p, b, c)
			preparedRebindShadow(t, p, b, c)
			run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
			run(fixture.Exec("CREATE USER rb_reader", 0))
			// Native schema SQL GRANT/REVOKE is absent. Explicit schema ownership
			// supplies reader visibility without transferring bootstrap-owned
			// tables; PostgreSQL exercises the real schema-USAGE transition.
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA rb_late TO rb_reader", 0))
			} else {
				run(fixture.Exec("ALTER SCHEMA rb_early OWNER TO rb_reader", 0))
				run(fixture.Exec("ALTER SCHEMA rb_late OWNER TO rb_reader", 0))
			}
			for _, q := range []string{"GRANT SELECT ON rb_early.t TO rb_reader", "GRANT SELECT ON rb_late.t TO rb_reader", "SET SESSION AUTHORIZATION rb_reader"} {
				run(fixture.Exec(q, 0))
			}
			first, schema := int64(9), "rb_early"
			if b.PG {
				first, schema = 7, "rb_late"
			}
			run(fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{schema}}))
			routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_acl AS SELECT v FROM t", "EXECUTE rb_acl")
			qualified := preparedRebindRoutes(t, p, b, c, "SELECT v FROM rb_late.t", "PREPARE rb_qualified AS SELECT v FROM rb_late.t", "EXECUTE rb_qualified")
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{first}})
			}
			if b.PG {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON SCHEMA rb_early TO rb_reader", "SET SESSION AUTHORIZATION rb_reader"} {
					run(fixture.Exec(q, 0))
				}
			}
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			}
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE SELECT ON rb_early.t FROM rb_reader", "SET SESSION AUTHORIZATION rb_reader"} {
				run(fixture.Exec(q, 0))
			}
			for _, r := range routes {
				preparedRebindFailure(t, p, b, r, sqlPreparedDiagnostic("42501", "permission denied for table t"))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT ON rb_early.t TO rb_reader", 0))
			if b.PG {
				run(fixture.Exec("REVOKE USAGE ON SCHEMA rb_late FROM rb_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION rb_reader", 0))
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			}
			for i, r := range qualified {
				if b.PG {
					d := sqlPreparedDiagnostic("42501", "permission denied for schema rb_late")
					d.Position = []string{"15", "39"}[i]
					preparedRebindFailure(t, p, b, r, d)
				} else {
					preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				}
			}
			if b.PG {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON SCHEMA rb_late TO rb_reader", "SET SESSION AUTHORIZATION rb_reader"} {
					run(fixture.Exec(q, 0))
				}
			}
			for _, r := range qualified {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
		})
	})

	t.Run("user_identity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rb_reader", "rb_reader2"})
		preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
			for _, q := range []string{"CREATE USER rb_reader", "CREATE USER rb_reader2", "CREATE SCHEMA rb_reader AUTHORIZATION rb_reader", "CREATE SCHEMA rb_reader2 AUTHORIZATION rb_reader2", "SET SESSION AUTHORIZATION rb_reader", "CREATE TABLE rb_reader.t(v INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO rb_reader.t VALUES(7)", 1))
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION rb_reader2", "CREATE TABLE rb_reader2.t(v INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO rb_reader2.t VALUES(9)", 1))
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION rb_reader", `SET search_path TO "$user",public`} {
				run(fixture.Exec(q, 0))
			}
			routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t", "PREPARE rb_user AS SELECT v FROM t", "EXECUTE rb_user")
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rb_reader2", 0))
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			}
		})
	})
}
