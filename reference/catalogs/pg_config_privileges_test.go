package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func pgConfigDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string) {
	t.Helper()
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_config"})
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func pgConfigFunctionDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string) {
	t.Helper()
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function pg_config"})
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	s.Prepared = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func pgConfigInitialACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, live bool) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	acl := "{" + owner + "=arwdDxtm/" + owner + "}"
	if live {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_config'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{acl}}))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_class'::regclass AND objoid='pg_catalog.pg_config'::regclass AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", acl}}))
	routineACL := "{" + owner + "=X/" + owner + "}"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT classoid::bigint,objoid::bigint,objsubid::int4,privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_proc'::regclass AND objoid=3400 ORDER BY objsubid", []string{"classoid", "objoid", "objsubid", "privtype", "initprivs"}, []string{"INT8", "INT8", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1255), int64(3400), int64(0), "i", routineACL}}))
}

func pgConfigRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	s := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,proowner::regrole::text,prolang::bigint,procost,prorows,provariadic::bigint,prosupport::text,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proargdefaults::text,protrftypes::text,prosrc,probin,prosqlbody::text,proconfig::text,proacl::text FROM pg_proc WHERE oid=3400",
		[]string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proargdefaults", "protrftypes", "prosrc", "probin", "prosqlbody", "proconfig", "proacl"},
		[]string{"INT8", "TEXT", "INT8", "TEXT", "INT8", "FLOAT4", "FLOAT4", "INT8", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(3400), "pg_config", int64(11), owner, int64(12), float64(1), float64(23), int64(0), "-", "f", false, false, true, true, "s", "r", int64(0), int64(0), int64(2249), "", "{25,25}", "{o,o}", "{name,setting}", nil, nil, "pg_config", nil, nil, nil, "{" + owner + "=X/" + owner + "}"}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}

func pgConfigPrivilegeFlags(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, selectOK, executeOK bool) {
	t.Helper()
	s := fixture.Select("SELECT has_table_privilege('pgconfig_actor','pg_catalog.pg_config','SELECT') AS can_select,has_function_privilege('pgconfig_actor','pg_catalog.pg_config()','EXECUTE') AS can_execute", []string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{selectOK, executeOK}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGConfigPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgconfig_actor"})
	pgConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
		exec := func(e fixture.Executor, query string) {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(query, 0))
		}
		exec(admin, "CREATE ROLE pgconfig_actor")
		exec(admin, "CREATE TABLE pgconfig_sentinel(n integer)")
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO pgconfig_sentinel VALUES(42)", 1))
		defer actor.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		defer admin.ExecContext(context.Background(), "REVOKE SELECT(name,setting) ON pg_catalog.pg_config FROM pgconfig_actor")
		defer admin.ExecContext(context.Background(), "REVOKE SELECT ON pg_catalog.pg_config FROM pgconfig_actor")
		defer admin.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_config() FROM pgconfig_actor")
		t.Run("default ACL", func(t *testing.T) { pgConfigInitialACL(t, p, b, admin, true) })
		pgConfigRoutineMetadata(t, p, b, admin)
		pgConfigPrivilegeFlags(t, p, b, admin, false, false)
		query := pgConfigSelect + " WHERE name='VERSION'"
		stmt, err := actor.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: query}
		pgConfigRead(t, p, b, held)
		exec(actor, "SET SESSION AUTHORIZATION pgconfig_actor")
		pgConfigDenied(t, p, b, actor, query)
		pgConfigDenied(t, p, b, held, query)
		ordinary, err := actor.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("ordinary Prepare", err)
		}
		defer ordinary.Close()
		pgConfigDenied(t, p, b, fixture.HeldStatement{Executor: actor, Statement: ordinary, SQL: query}, query)
		for _, role := range []string{"pg_read_all_stats", "pg_monitor"} {
			exec(admin, "GRANT "+role+" TO pgconfig_actor")
			pgConfigDenied(t, p, b, actor, query)
			pgConfigDenied(t, p, b, held, query)
			exec(admin, "REVOKE "+role+" FROM pgconfig_actor")
		}
		exec(admin, "GRANT SELECT ON pg_catalog.pg_config TO pgconfig_actor")
		pgConfigPrivilegeFlags(t, p, b, admin, true, false)
		pgConfigFunctionDenied(t, p, b, actor, query)
		pgConfigFunctionDenied(t, p, b, held, query)
		exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_config() TO pgconfig_actor")
		pgConfigPrivilegeFlags(t, p, b, admin, true, true)
		pgConfigRead(t, p, b, actor)
		pgConfigRead(t, p, b, held)
		fresh := pgConfigStep(query, pgConfigVersion(b))
		fresh.Prepared = true
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, fresh)
		t.Run("history after grant", func(t *testing.T) { pgConfigInitialACL(t, p, b, admin, false) })
		exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_config() FROM pgconfig_actor")
		pgConfigPrivilegeFlags(t, p, b, admin, true, false)
		pgConfigFunctionDenied(t, p, b, actor, query)
		pgConfigFunctionDenied(t, p, b, held, query)
		exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_config() TO pgconfig_actor")
		exec(admin, "REVOKE SELECT ON pg_catalog.pg_config FROM pgconfig_actor")
		pgConfigPrivilegeFlags(t, p, b, admin, false, true)
		pgConfigDenied(t, p, b, actor, query)
		pgConfigDenied(t, p, b, held, query)
		exec(admin, "GRANT SELECT(name) ON pg_catalog.pg_config TO pgconfig_actor")
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT name FROM pg_catalog.pg_config WHERE name='VERSION'", []string{"name"}, []string{"TEXT"}, [][]any{{"VERSION"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
		}
		pgConfigDenied(t, p, b, actor, "SELECT * FROM pg_catalog.pg_config")
		pgConfigDenied(t, p, b, held, query)
		exec(admin, "GRANT SELECT(setting) ON pg_catalog.pg_config TO pgconfig_actor")
		pgConfigRead(t, p, b, actor)
		pgConfigRead(t, p, b, held)
		exec(admin, "REVOKE SELECT(name,setting) ON pg_catalog.pg_config FROM pgconfig_actor")
		pgConfigDenied(t, p, b, actor, query)
		pgConfigDenied(t, p, b, held, query)
		t.Run("history after revoke", func(t *testing.T) { pgConfigInitialACL(t, p, b, admin, true) })
		exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_config() FROM pgconfig_actor")
		pgConfigPrivilegeFlags(t, p, b, admin, false, false)
		pgConfigDenied(t, p, b, actor, query)
		pgConfigDenied(t, p, b, held, query)
		exec(actor, "RESET SESSION AUTHORIZATION")
		pgConfigRead(t, p, b, actor)
		pgConfigSentinel(t, p, b, admin)
	})
}
