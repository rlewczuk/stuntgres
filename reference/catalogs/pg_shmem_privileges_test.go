package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"stuntgres/reference/fixture"
)

func shmemDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, i int) {
	t.Helper()
	s := fixture.ImplicitError("SELECT * FROM pg_catalog."+shmemNames[i], fixture.Diagnostic{Code:"42501", Message:"permission denied for view "+shmemNames[i]})
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func shmemPrivileges(t *testing.T, observe bool) {
	fixture.RoleReferenceCleanup(t, []string{"shmem_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		a, admin := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		pgSequenceExec(t, p, b, admin, "CREATE ROLE shmem_actor")
		pgSequenceExec(t, p, b, admin, "CREATE TABLE shmem_sentinel (n integer)")
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO shmem_sentinel VALUES (42)", 1))
		for i, name := range shmemNames {
			shmemDefaultACL(t, p, admin, name, b.PG)
			defer admin.ExecContext(context.Background(), "REVOKE SELECT ON pg_catalog."+name+" FROM shmem_actor")
			function := "pg_get_shmem_allocations"
			if i == 1 { function = "pg_get_shmem_allocations_numa" }
			defer admin.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_catalog."+function+"() FROM shmem_actor")
			q := "SELECT * FROM pg_catalog."+name
			stmt, err := a.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }
			held := fixture.HeldStatement{Executor:a, Statement:stmt, SQL:q}
			shmemRead(t, p, b, held, i, false, observe)
			pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION shmem_actor")
			shmemDenied(t, p, b, a, i); shmemDenied(t, p, b, held, i)
			// Parse/prepare does not retain permission; execution checks current authority.
			ordinary, err := a.PrepareContext(p.Ctx, q); if err != nil { t.Fatal("ordinary prepare", err) }
			shmemDenied(t, p, b, fixture.HeldStatement{Executor:a, Statement:ordinary, SQL:q}, i)
			ordinary.Close()
			for _, role := range []string{"pg_read_all_stats", "pg_monitor"} {
				pgSequenceExec(t, p, b, admin, "GRANT "+role+" TO shmem_actor")
				shmemRead(t, p, b, held, i, false, false)
				pgSequenceExec(t, p, b, admin, "REVOKE "+role+" FROM shmem_actor")
				shmemDenied(t, p, b, held, i)
			}
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_stats TO shmem_actor WITH INHERIT FALSE, SET TRUE")
			shmemDenied(t, p, b, held, i)
			pgSequenceExec(t, p, b, a, "SET ROLE pg_read_all_stats")
			shmemRead(t, p, b, held, i, false, false)
			pgSequenceExec(t, p, b, a, "RESET ROLE")
			shmemDenied(t, p, b, held, i)
			pgSequenceExec(t, p, b, admin, "REVOKE pg_read_all_stats FROM shmem_actor")
			pgSequenceExec(t, p, b, admin, "ALTER ROLE shmem_actor NOINHERIT")
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_stats TO shmem_actor")
			shmemDenied(t, p, b, held, i)
			pgSequenceExec(t, p, b, a, "SET ROLE pg_read_all_stats")
			shmemRead(t, p, b, held, i, false, false)
			pgSequenceExec(t, p, b, a, "RESET ROLE")
			pgSequenceExec(t, p, b, admin, "REVOKE pg_read_all_stats FROM shmem_actor")
			pgSequenceExec(t, p, b, admin, "ALTER ROLE shmem_actor INHERIT")
			pgSequenceExec(t, p, b, admin, "GRANT SELECT ON pg_catalog."+name+" TO shmem_actor")
			shmemFunctionDenied(t, p, b, a, i, function)
			shmemFunctionDenied(t, p, b, held, i, function)
			pgSequenceExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog."+name+" FROM shmem_actor")
			pgSequenceExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog."+function+"() TO shmem_actor")
			shmemDenied(t, p, b, a, i)
			shmemDenied(t, p, b, held, i)
			pgSequenceExec(t, p, b, admin, "GRANT SELECT ON pg_catalog."+name+" TO shmem_actor")
			shmemRead(t, p, b, a, i, false, false)
			shmemRead(t, p, b, held, i, false, false)
			var retained *sql.Rows
			if !b.PG {
				retained, err = stmt.QueryContext(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer retained.Close()
			}
			pgSequenceExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog."+function+"() FROM shmem_actor")
			shmemFunctionDenied(t, p, b, a, i, function)
			shmemFunctionDenied(t, p, b, held, i, function)
			if observe { fmt.Fprintf(os.Stderr, "SHMEM %s held EXECUTE revoke: 42501 permission denied for function %s\n", b.Name, function) }
			pgSequenceExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog."+function+"() TO shmem_actor")
			shmemRead(t, p, b, held, i, false, false)
			pgSequenceExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog."+name+" FROM shmem_actor")
			shmemDenied(t, p, b, held, i)
			pgSequenceExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog."+function+"() FROM shmem_actor")
			if retained != nil { shmemRetained(t, retained, i) }
			if observe { fmt.Fprintf(os.Stderr, "SHMEM %s held revoke: 42501 permission denied for view %s\n", b.Name, name) }
			pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
			stmt.Close()
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT n FROM shmem_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
		if observe { fmt.Fprintf(os.Stderr, "SHMEM %s sentinel unchanged: 42\n", b.Name) }
	}) }
}

func TestReferencePGShmemPrivileges(t *testing.T) { shmemPrivileges(t, false) }
func TestReferencePGShmemFeatureSmoke(t *testing.T) { shmemPrivileges(t, true) }

func shmemDefaultACL(t *testing.T, p *fixture.Pair, e fixture.Executor, name string, pg bool) {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT relacl::text FROM pg_class WHERE relname=$1", name)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	var acl string
	if !rows.Next() { t.Fatal("missing ACL", rows.Err()) }
	if err := rows.Scan(&acl); err != nil { t.Fatal(err) }
	owner := "postgres"
	if pg { owner = "stuntgres" }
	want := "{"+owner+"=arwdDxtm/"+owner+",pg_read_all_stats=r/"+owner+"}"
	if acl != want { t.Fatal("exact ACL", name, acl) }
	if rows.Next() || rows.Err() != nil { t.Fatal("ACL cardinality", rows.Err()) }
}

func shmemFunctionDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, index int, function string) {
	t.Helper()
	s := fixture.ImplicitError("SELECT * FROM pg_catalog."+shmemNames[index], fixture.Diagnostic{Code:"42501", Message:"permission denied for function "+function})
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
