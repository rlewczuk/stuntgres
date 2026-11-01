package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func identInitialACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, live bool) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	tableACL := "{" + owner + "=arwdDxtm/" + owner + "}"
	routineACL := "{" + owner + "=X/" + owner + "}"
	if live {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_ident_file_mappings'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{tableACL}}))
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=6250", []string{"proacl"}, []string{"TEXT"}, [][]any{{routineACL}}))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_class'::regclass AND objoid='pg_catalog.pg_ident_file_mappings'::regclass AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", tableACL}}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_proc'::regclass AND objoid=6250 AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", routineACL}}))
}
func identDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, function bool) {
	t.Helper()
	message := "permission denied for view pg_ident_file_mappings"
	if function {
		message = "permission denied for function pg_ident_file_mappings"
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: message})
		s.SelectRows = true
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func TestReferencePGIdentFileMappingsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ident_actor"})
	p, files := fixture.NewIdentFileScenario(t, "secret os_secret db_secret\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			run(admin, "CREATE ROLE ident_actor")
			run(admin, "CREATE TABLE ident_sentinel(n integer)")
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO ident_sentinel VALUES(42)", 1))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if _, err := actor.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{
					"REVOKE pg_read_all_stats,pg_monitor FROM ident_actor",
					"REVOKE SELECT(map_number,file_name,line_number,map_name,sys_name,pg_username,error) ON pg_catalog.pg_ident_file_mappings FROM ident_actor",
					"REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM ident_actor",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM ident_actor",
				} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			stmt, err := actor.PrepareContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: identSelect}
			identInitialACL(t, p, b, admin, true)
			run(actor, "SET SESSION AUTHORIZATION ident_actor")
			ordinary, err := actor.PrepareContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer ordinary.Close()
			current := fixture.HeldStatement{Executor: actor, Statement: ordinary, SQL: identSelect}
			denied := func(function bool) {
				for _, e := range []fixture.Executor{actor, held, current} {
					identDenied(t, p, b, e, identSelect, function)
				}
			}
			flags := func(table, function bool) {
				fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT has_table_privilege('ident_actor','pg_catalog.pg_ident_file_mappings','SELECT') AS can_select,has_function_privilege('ident_actor','pg_catalog.pg_ident_file_mappings()','EXECUTE') AS can_execute", []string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{table, function}}))
			}
			flags(false, false)
			denied(false)
			identDenied(t, p, b, actor, "SELECT 1 FROM pg_catalog.pg_ident_file_mappings", false)
			for _, role := range []string{"pg_read_all_stats", "pg_monitor"} {
				run(admin, "GRANT "+role+" TO ident_actor")
				denied(false)
				run(admin, "REVOKE "+role+" FROM ident_actor")
			}
			run(admin, "GRANT SELECT ON pg_catalog.pg_ident_file_mappings TO ident_actor")
			flags(true, false)
			denied(true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO ident_actor")
			flags(true, true)
			want := [][]any{identValid(1, files.Path(b, ""), 1, "secret", "os_secret", "db_secret")}
			for _, e := range []fixture.Executor{actor, held, current} {
				identRead(t, p, b, e, identSelect, want, true)
			}
			identInitialACL(t, p, b, admin, false)
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM ident_actor")
			denied(true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO ident_actor")
			run(admin, "REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM ident_actor")
			denied(false)
			run(admin, "GRANT SELECT(file_name) ON pg_catalog.pg_ident_file_mappings TO ident_actor")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT file_name FROM pg_catalog.pg_ident_file_mappings", []string{"file_name"}, []string{"TEXT"}, [][]any{{files.Path(b, "")}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			identDenied(t, p, b, actor, "SELECT file_name FROM pg_catalog.pg_ident_file_mappings WHERE map_name='secret'", false)
			identDenied(t, p, b, actor, "SELECT file_name FROM pg_catalog.pg_ident_file_mappings ORDER BY map_number", false)
			denied(false)
			run(admin, "GRANT SELECT(map_number,line_number,map_name,sys_name,pg_username,error) ON pg_catalog.pg_ident_file_mappings TO ident_actor")
			identRead(t, p, b, held, identSelect, want, true)
			run(admin, "REVOKE SELECT(map_number,file_name,line_number,map_name,sys_name,pg_username,error) ON pg_catalog.pg_ident_file_mappings FROM ident_actor")
			files.Remove(t, p.Ctx, b, "")
			// Neither rejected admission may leak the now-missing sensitive path.
			denied(false)
			run(admin, "GRANT SELECT ON pg_catalog.pg_ident_file_mappings TO ident_actor")
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM ident_actor")
			denied(true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO ident_actor")
			s := fixture.ImplicitError(identSelect, fixture.Diagnostic{Code: "58P01", Message: "could not open file \"" + files.Path(b, "") + "\": No such file or directory"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			files.Write(t, p.Ctx, b, "", "secret os_secret db_secret\n")
			run(admin, "REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM ident_actor")
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM ident_actor")
			flags(false, false)
			run(actor, "RESET SESSION AUTHORIZATION")
			identRead(t, p, b, held, identSelect, want, true)
			identInitialACL(t, p, b, admin, true)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT n FROM ident_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
		})
	}
}
