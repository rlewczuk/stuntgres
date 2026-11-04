package reference_test

import (
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hbaDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, function bool) {
	t.Helper()
	message := "permission denied for view pg_hba_file_rules"
	if function {
		message = "permission denied for function pg_hba_file_rules"
	}
	for _, prepared := range []bool{false, true} {
		hbaSQLError(t, p, b, e, query, fixture.Diagnostic{Code: "42501", Message: message}, prepared, "execute")
	}
}
func TestReferencePGHBAFileRulesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hba_actor"})
	p, f := fixture.NewHBAFileScenario(t, fixture.HBARuleSet{
		Contents: "local all all ldap ldapbasedn=d ldapbindpasswd=secret\nbad\n",
		Rules: []stuntgres.HBARule{
			hbaLocalFact("{MAIN}", "ldap", "ldapbasedn=d", "ldapbindpasswd=secret", "ldapscope=2"),
			{FileName: new("{MAIN}"), LineNumber: new(int32(2)), Error: new(`invalid connection type "bad"`)},
		},
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			run(admin, "CREATE ROLE hba_actor")
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if _, err := actor.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{
					"REVOKE pg_read_all_stats,pg_read_all_settings,pg_monitor FROM hba_actor",
					"REVOKE SELECT(rule_number,file_name,line_number,type,database,user_name,address,netmask,auth_method,options,error) ON pg_catalog.pg_hba_file_rules FROM hba_actor",
					"REVOKE SELECT ON pg_catalog.pg_hba_file_rules FROM hba_actor",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() FROM hba_actor",
				} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			stmt, err := actor.PrepareContext(p.Ctx, hbaSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: hbaSelect}
			hbaInitialACL(t, p, b, admin, true)
			run(actor, "SET SESSION AUTHORIZATION hba_actor")
			ordinary, err := actor.PrepareContext(p.Ctx, hbaSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer ordinary.Close()
			current := fixture.HeldStatement{Executor: actor, Statement: ordinary, SQL: hbaSelect}
			denied := func(function bool) {
				for _, e := range []fixture.Executor{actor, held, current} {
					hbaDenied(t, p, b, e, hbaSelect, function)
				}
			}
			denied(false)
			hbaDenied(t, p, b, actor, "SELECT 1 FROM pg_catalog.pg_hba_file_rules WHERE false", false)
			for _, role := range []string{"pg_monitor", "pg_read_all_stats", "pg_read_all_settings"} {
				run(admin, "GRANT "+role+" TO hba_actor")
				denied(false)
				run(admin, "REVOKE "+role+" FROM hba_actor")
			}
			run(admin, "GRANT SELECT ON pg_catalog.pg_hba_file_rules TO hba_actor")
			denied(true)
			hbaDenied(t, p, b, actor, "SELECT 1 AS one FROM pg_catalog.pg_hba_file_rules", true)
			// A proven-false filter needs SELECT, but never opens the
			// populated source or demands its backing routine's EXECUTE.
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT 1 FROM pg_catalog.pg_hba_file_rules WHERE false", []string{"?column?"}, []string{"INT4"}, nil)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() TO hba_actor")
			main := f.Path(b, "")
			want := [][]any{hbaLocal(1, main, 1, "ldap", "ldapbasedn=d", "ldapbindpasswd=secret", "ldapscope=2"), hbaInvalid(main, 2, `invalid connection type "bad"`)}
			for _, e := range []fixture.Executor{actor, held, current} {
				hbaRead(t, p, b, e, hbaSelect, want, true)
			}
			hbaInitialACL(t, p, b, admin, false)
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() FROM hba_actor")
			denied(true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() TO hba_actor")
			run(admin, "REVOKE SELECT ON pg_catalog.pg_hba_file_rules FROM hba_actor")
			denied(false)
			run(admin, "GRANT SELECT(file_name) ON pg_catalog.pg_hba_file_rules TO hba_actor")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT file_name FROM pg_catalog.pg_hba_file_rules", []string{"file_name"}, []string{"TEXT"}, [][]any{{main}, {main}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
				// PostgreSQL's any-column grant admits a constant projection, but
				// never bypasses routine authority or producer execution.
				s = fixture.Select("SELECT 1 AS one FROM pg_catalog.pg_hba_file_rules", []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			hbaDenied(t, p, b, actor, "SELECT file_name FROM pg_catalog.pg_hba_file_rules WHERE auth_method='ldap'", false)
			hbaDenied(t, p, b, actor, "SELECT file_name FROM pg_catalog.pg_hba_file_rules ORDER BY rule_number", false)
			denied(false)
			run(admin, "GRANT SELECT(rule_number,line_number,type,database,user_name,address,netmask,auth_method,options,error) ON pg_catalog.pg_hba_file_rules TO hba_actor")
			hbaRead(t, p, b, held, hbaSelect, want, true)
			run(admin, "REVOKE SELECT(rule_number,file_name,line_number,type,database,user_name,address,netmask,auth_method,options,error) ON pg_catalog.pg_hba_file_rules FROM hba_actor")
			f.Install(t, p.Ctx, b, fixture.HBARuleSet{})
			denied(false)
			run(admin, "GRANT SELECT ON pg_catalog.pg_hba_file_rules TO hba_actor")
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() FROM hba_actor")
			denied(true)
			// Constant projection still opens the producer, even with no rows.
			hbaDenied(t, p, b, actor, "SELECT 1 AS one FROM pg_catalog.pg_hba_file_rules", true)
			// A proven-false filter keeps relation SELECT admission, but never
			// opens the producer or demands its backing routine's EXECUTE.
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT 1 FROM pg_catalog.pg_hba_file_rules WHERE false", []string{"?column?"}, []string{"INT4"}, nil)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			run(admin, "REVOKE SELECT ON pg_catalog.pg_hba_file_rules FROM hba_actor")
			denied(false)
			hbaDenied(t, p, b, actor, "SELECT 1 AS one FROM pg_catalog.pg_hba_file_rules WHERE false", false)
			run(admin, "GRANT SELECT ON pg_catalog.pg_hba_file_rules TO hba_actor")
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() TO hba_actor")
			hbaRead(t, p, b, actor, hbaSelect, nil, true)
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all trust\n", "trust"))
			run(actor, "RESET SESSION AUTHORIZATION")
			hbaRead(t, p, b, held, hbaSelect, [][]any{hbaLocal(1, main, 1, "trust")}, true)
		})
	}
}
