package reference_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"stuntgres/reference/fixture"
)

func aiosDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, function bool) {
	t.Helper()
	message := "permission denied for view pg_aios"
	if function { message = "permission denied for function pg_get_aios" }
	s := fixture.ImplicitError(aiosQuery, fixture.Diagnostic{Code:"42501", Message:message})
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func aiosAuthorization(t *testing.T, backing, observe bool) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"aios_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
		defer actor.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		pgSequenceExec(t, p, b, admin, "CREATE ROLE aios_actor")
		pgSequenceExec(t, p, b, admin, "CREATE TABLE aios_sentinel(n integer)")
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO aios_sentinel VALUES(42)",1))
		defer admin.ExecContext(context.Background(), "REVOKE SELECT ON pg_catalog.pg_aios FROM aios_actor")
		defer admin.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_aios() FROM aios_actor")
		stmt, err := actor.PrepareContext(p.Ctx, aiosQuery); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:actor, Statement:stmt, SQL:aiosQuery}
		aiosRead(t, p, b, held, false)
		aiosCount(t, p, b, admin)
		pgSequenceExec(t, p, b, actor, "SET SESSION AUTHORIZATION aios_actor")
		aiosDenied(t, p, b, actor, false); aiosDenied(t, p, b, held, false)
		ordinary, err := actor.PrepareContext(p.Ctx, aiosQuery); if err != nil { t.Fatal("ordinary prepare", err) }
		aiosDenied(t, p, b, fixture.HeldStatement{Executor:actor, Statement:ordinary, SQL:aiosQuery}, false)
		ordinary.Close()
		if !backing || observe {
			for _, role := range []string{"pg_read_all_stats","pg_monitor"} {
				pgSequenceExec(t, p, b, admin, "GRANT "+role+" TO aios_actor")
				aiosRead(t, p, b, actor, false); aiosRead(t, p, b, held, false)
				pgSequenceExec(t, p, b, admin, "REVOKE "+role+" FROM aios_actor")
				aiosDenied(t, p, b, held, false)
			}
			for _, membership := range []string{"GRANT pg_read_all_stats TO aios_actor WITH INHERIT FALSE, SET TRUE","GRANT pg_read_all_stats TO aios_actor"} {
				if membership == "GRANT pg_read_all_stats TO aios_actor" { pgSequenceExec(t, p, b, admin, "ALTER ROLE aios_actor NOINHERIT") }
				pgSequenceExec(t, p, b, admin, membership)
				aiosDenied(t, p, b, held, false)
				pgSequenceExec(t, p, b, actor, "SET ROLE pg_read_all_stats")
				aiosRead(t, p, b, held, false)
				pgSequenceExec(t, p, b, actor, "RESET ROLE")
				aiosDenied(t, p, b, held, false)
				pgSequenceExec(t, p, b, admin, "REVOKE pg_read_all_stats FROM aios_actor")
			}
			pgSequenceExec(t, p, b, admin, "ALTER ROLE aios_actor INHERIT")
		}
		if backing || observe {
			aiosInitialPrivileges(t, p, b, admin)
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_data TO aios_actor")
			aiosPrivilegeFlags(t, p, b, admin, true, false)
			aiosDenied(t, p, b, actor, true); aiosDenied(t, p, b, held, true)
			pgSequenceExec(t, p, b, admin, "REVOKE pg_read_all_data FROM aios_actor")
			pgSequenceExec(t, p, b, admin, "GRANT SELECT ON pg_catalog.pg_aios TO aios_actor")
			aiosPrivilegeFlags(t, p, b, admin, true, false)
			aiosDenied(t, p, b, actor, true); aiosDenied(t, p, b, held, true)
			pgSequenceExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_aios FROM aios_actor")
			pgSequenceExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_aios() TO aios_actor")
			aiosPrivilegeFlags(t, p, b, admin, false, true)
			aiosDenied(t, p, b, actor, false); aiosDenied(t, p, b, held, false)
			pgSequenceExec(t, p, b, admin, "GRANT SELECT ON pg_catalog.pg_aios TO aios_actor")
			aiosPrivilegeFlags(t, p, b, admin, true, true)
			aiosRead(t, p, b, actor, false); aiosRead(t, p, b, held, false)
			pgSequenceExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_aios() FROM aios_actor")
			aiosDenied(t, p, b, actor, true); aiosDenied(t, p, b, held, true)
			pgSequenceExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_aios() TO aios_actor")
			aiosRead(t, p, b, held, false)
			pgSequenceExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_aios FROM aios_actor")
			aiosDenied(t, p, b, held, false)
			aiosInitialPrivileges(t, p, b, admin)
		}
		pgSequenceExec(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		aiosRead(t, p, b, actor, true)
		aiosSentinel(t, p, b, admin)
		if observe { fmt.Fprintf(os.Stderr, "AIOS %s: 15 typed fields; native inventory=0 (PostgreSQL live inventory unconstrained); inherited stats allowed; ordinary view and SELECT-only/current held EXECUTE revoke denied 42501; sentinel=42\n", b.Name) }
	}) }
}

func aiosPrivilegeFlags(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, selectOK, executeOK bool) {
	t.Helper()
	s := fixture.Select("SELECT has_table_privilege('aios_actor','pg_catalog.pg_aios','SELECT') AS can_select,has_function_privilege('aios_actor','pg_catalog.pg_get_aios()','EXECUTE') AS can_execute", []string{"can_select","can_execute"}, []string{"BOOL","BOOL"}, [][]any{{selectOK,executeOK}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func aiosInitialPrivileges(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	owner := "postgres"; if b.PG { owner = "stuntgres" }
	for _, tc := range []struct{class, object, acl string}{
		{"pg_class","'pg_catalog.pg_aios'::regclass","{"+owner+"=arwdDxtm/"+owner+",pg_read_all_stats=r/"+owner+"}"},
		{"pg_proc","6399","{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner+"}"},
	} {
		s := fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog."+tc.class+"'::regclass AND objoid="+tc.object+" AND objsubid=0", []string{"privtype","initprivs"}, []string{"TEXT","TEXT"}, [][]any{{"i",tc.acl}})
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func TestReferencePGAIOSPrivileges(t *testing.T) { aiosAuthorization(t, false, false) }
func TestReferencePGAIOSBackingAuthority(t *testing.T) { aiosAuthorization(t, true, false) }
