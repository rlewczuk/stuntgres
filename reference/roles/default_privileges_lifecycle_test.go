package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesAtomicity(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_owner_ns", "dp_owner")
		cases := []struct{ q, code, message string }{
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader,dp_missing", "42704", `role "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner,dp_missing GRANT SELECT ON TABLES TO dp_reader", "42704", `role "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_owner_ns,dp_missing GRANT SELECT ON TABLES TO dp_reader", "3F000", `schema "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader,PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT USAGE ON TABLES TO dp_missing", "42704", `role "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_missing GRANT ALL ON SCHEMAS TO dp_reader", "3F000", `schema "dp_missing" does not exist`},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_owner_ns GRANT ALL ON SCHEMAS TO dp_reader", "0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON SCHEMAS"},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_owner_ns,dp_missing GRANT SELECT ON TABLES TO dp_reader,PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
			{"ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_missing,dp_owner_ns GRANT SELECT ON TABLES TO dp_reader,PUBLIC WITH GRANT OPTION", "3F000", `schema "dp_missing" does not exist`},
		}
		for i, tc := range cases {
			fixture.RoleGrantError(run, tc.q, tc.code, tc.message)
			name := []string{"dp_atomic_a", "dp_atomic_b", "dp_atomic_c", "dp_atomic_d", "dp_atomic_e", "dp_atomic_f", "dp_atomic_g", "dp_atomic_h", "dp_atomic_i"}[i]
			fixture.RoleGrantActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns."+name, 9) })
			fixture.RoleGrantActor(run, "dp_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_owner_ns."+name, "table", name) })
		}
		// The first creator/schema can produce a private candidate before the
		// second creator fails authority; none of that grant may be published.
		fixture.RoleGrantActor(run, "dp_owner", func() {
			fixture.RoleGrantError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner,dp_other IN SCHEMA dp_owner_ns GRANT SELECT ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
			dpTable(run, "dp_owner_ns.dp_atomic_creator", 9)
		})
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_owner_ns.dp_atomic_creator", "table", "dp_atomic_creator")
		})
		fixture.RoleGrantActor(run, "dp_member", func() {
			fixture.RoleGrantError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_missing GRANT SELECT ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
		})
		q := "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader; SELECT )"
		fixture.RoleGrantSyntax(run, q, ")", prepared)
		fixture.RoleGrantActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_suffix", 9) })
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_owner_ns.dp_suffix", "table", "dp_suffix")
		})
		// A real direct retained message shares candidate state and discards its prefix
		// on a late failure. Prepared multiple commands retain their parser boundary.
		q = "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader; CREATE TABLE dp_batch(x INT); INSERT INTO dp_batch VALUES(9)"
		if prepared {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
			s.ErrorStage = "prepare"
			run(s)
		} else {
			run(fixture.Exec(q, 1))
			fixture.RoleGrantActor(run, "dp_reader", func() { dpRead(run, "dp_batch", 9) })
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		q = "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader; CREATE TABLE dp_batch_failed(x INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_missing"
		if !prepared {
			fixture.RoleGrantError(run, q, "42704", `role "dp_missing" does not exist`)
			s := fixture.ImplicitError("SELECT x FROM dp_batch_failed", fixture.Diagnostic{Code: "42P01", Message: `relation "dp_batch_failed" does not exist`, Position: "15"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		dpTable(run, "dp_batch_after", 11)
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_batch_after", "table", "dp_batch_after")
		})
	})
}

func TestReferenceRoleDefaultPrivilegesDependencies(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		dpDependency(run, "dp_owner", "relations", "dp_owner", "", true)
		dpDependency(run, "dp_reader", "relations", "dp_owner", "", false)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("DROP ROLE dp_owner", 0))
		run(fixture.Exec("CREATE ROLE dp_owner", 0))
		run(fixture.Exec("DROP ROLE dp_reader", 0))
		run(fixture.Exec("CREATE ROLE dp_reader", 0))
		run(fixture.Exec("CREATE SCHEMA dp_s AUTHORIZATION dp_other", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s GRANT SELECT ON TABLES TO dp_reader", 0))
		dpDependency(run, "dp_owner", "relations", "dp_owner", "dp_s", true)
		dpDependency(run, "dp_reader", "relations", "dp_owner", "dp_s", false)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("DROP ROLE dp_owner", 0))
		run(fixture.Exec("CREATE ROLE dp_owner", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_materialized", 7)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		s := fixture.ImplicitError("DROP ROLE dp_reader", fixture.Diagnostic{Code: "2BP01", Message: `role "dp_reader" cannot be dropped because some objects depend on it`, Detail: "privileges for table dp_materialized"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("DROP TABLE dp_materialized", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT USAGE ON TYPES TO dp_reader", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_composite_dep AS(x INT)", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE USAGE ON TYPES FROM dp_reader", 0))
		s = fixture.ImplicitError("DROP ROLE dp_reader", fixture.Diagnostic{Code: "2BP01", Message: `role "dp_reader" cannot be dropped because some objects depend on it`, Detail: "privileges for type dp_s.dp_composite_dep"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("DROP SCHEMA dp_s CASCADE", 0))
		run(fixture.Exec("DROP ROLE dp_reader", 0))
		run(fixture.Exec("CREATE ROLE dp_reader", 0))
	})
}

func TestReferenceRoleDefaultPrivilegesLifecycle(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_s", "dp_reader")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT INSERT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_s.dp_before", 7)
		run(fixture.Exec("ALTER SCHEMA dp_s RENAME TO dp_renamed", 0))
		dpTable(run, "dp_renamed.dp_after_rename", 9)
		fixture.RoleGrantActor(run, "dp_reader", func() {
			dpRead(run, "dp_renamed.dp_after_rename", 9)
			run(fixture.Exec("INSERT INTO dp_renamed.dp_after_rename VALUES(11)", 1))
		})
		run(fixture.Exec("DROP SCHEMA dp_renamed CASCADE", 0))
		dpSchema(run, "dp_s", "dp_reader")
		dpTable(run, "dp_s.dp_after_recreate", 13)
		fixture.RoleGrantActor(run, "dp_reader", func() {
			dpRead(run, "dp_s.dp_after_recreate", 13)
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO dp_s.dp_after_recreate VALUES(17)", "table", "dp_after_recreate")
		})
		// Go Prepare before a recipient exists is effect-free. Resolve at each Execute.
		run(fixture.Exec("DROP ROLE dp_future", 0))
		q := "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_future"
		utility, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("effect-free utility Prepare", err)
		}
		defer utility.Close()
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		symbolic, err := nc.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet unresolved Prepare", err)
		}
		defer symbolic.Close()
		if len(notices) != 0 {
			t.Fatal("utility Prepare notices", notices)
		}
		run(fixture.Exec("CREATE ROLE dp_future", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: utility, SQL: q}, b.Recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION dp_owner", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: nc, Statement: symbolic, SQL: q}, recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if len(notices) != 0 {
			t.Fatal("utility Execute notices", notices)
		}
		dpSchema(run, "dp_owner_ns", "dp_owner")
		fixture.RoleGrantActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_fresh_actor", 23) })
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_fresh_actor' AND grantee='dp_future'", [][]any{{"dp_owner", "dp_future", "dp_owner_ns", "dp_fresh_actor", "SELECT", "NO", "YES"}})
		dpTable(run, "dp_held", 19)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_future", 0))
		fixture.RoleGrantActor(run, "dp_future", func() { dpRead(run, "dp_held", 19) })
		fixture.RoleGrantActor(run, "dp_reader", func() {
			query := "SELECT x FROM dp_held ORDER BY x"
			held, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			source := fixture.HeldStatement{Executor: c, Statement: held, SQL: query}
			want := fixture.Select(query, []string{"x"}, []string{"INT4"}, [][]any{{int64(19)}})
			fixture.RunStep(t, p.Ctx, source, b.Recorder, want)
			old, err := held.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			admin, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			// Default reversal never changes this object's ACL or returned snapshot.
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
			fixture.DeleteReferenceConsume(t, b.PG, old, nil, want)
			fixture.RunStep(t, p.Ctx, source, b.Recorder, want)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("REVOKE SELECT ON dp_held FROM dp_reader", 0))
			fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table dp_held"}))
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("GRANT SELECT ON dp_held TO dp_reader", 0))
			fixture.RunStep(t, p.Ctx, source, b.Recorder, want)
		})
		// The information-schema source itself has immutable returned rows and fresh
		// execution authority/ACL snapshots, not just ordinary table consumers.
		owner, database := fixture.RoleGrantEnvironment(t, p, c)
		query := "SELECT " + dpPrivilegeFields + " FROM information_schema.table_privileges WHERE table_schema='public' AND table_name='dp_held' AND grantee='dp_reader'" + dpPrivilegeOrder
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		source := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		want := fixture.Select(query, dpPrivilegeColumns, dpPrivilegeTypes, [][]any{{owner, "dp_reader", database, "public", "dp_held", "SELECT", "NO", "YES"}})
		fixture.RunStep(t, p.Ctx, source, b.Recorder, want)
		old, err := statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("REVOKE SELECT ON dp_held FROM dp_reader", 0))
		fixture.DeleteReferenceConsume(t, b.PG, old, nil, want)
		empty := want
		empty.Rows = nil
		fixture.RunStep(t, p.Ctx, source, b.Recorder, empty)
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("GRANT SELECT ON dp_held TO dp_reader", 0))
		fixture.RunStep(t, p.Ctx, source, b.Recorder, want)
		// Reversing the creator's policy must not revoke materialized grants.
		// Ownership transfer must also release dependent-type ACL origins so
		// this otherwise unencumbered former creator can retire with the view live.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_set GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER SCHEMA public OWNER TO dp_set", 0))
		fixture.RoleGrantActor(run, "dp_set", func() {
			run(fixture.Exec("CREATE MATERIALIZED VIEW dp_owner_transfer7 AS SELECT 7 AS x", 1))
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_set REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW dp_owner_transfer7 OWNER TO dp_other", 0))
		run(fixture.Exec("ALTER SCHEMA public OWNER TO "+owner, 0))
		run(fixture.Exec("DROP ROLE dp_set", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_transfer7", 7)
		})
		fixture.RoleGrantActor(run, "dp_other", func() {
			run(fixture.Exec("REFRESH MATERIALIZED VIEW dp_owner_transfer7", 0))
			dpRead(run, "dp_owner_transfer7", 7)
		})
		fixture.RoleGrantActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_transfer7", 7)
		})
	})
}
