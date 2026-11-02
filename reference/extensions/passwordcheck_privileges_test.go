package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckPrivileges(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_acl_login", "pc_acl_parent"}, func(p *fixture.Pair, b fixture.Backend, admin *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				ra := passwordCheckRun(t, p, admin, r, prepared)
				ra(fixture.Exec("CREATE ROLE pc_acl_login LOGIN CREATEROLE PASSWORD 'role-attributes-fixture'", 0))
				ra(fixture.Exec("CREATE ROLE pc_acl_parent", 0))
				pool := fixture.RoleAttributesPool(t, p, b, "pc_acl_login")
				c, err := pool.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer c.Close()
				run := passwordCheckRun(t, p, c, b.Recorder, prepared)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					for _, role := range []string{"pc_acl_login", "pc_acl_parent"} {
						if _, err := b.DB.ExecContext(ctx, "DROP OWNED BY "+role+" CASCADE"); err != nil { t.Error("parameter ACL cleanup", err) }
					}
				})
				denied := func(query string) fixture.Step {
					return fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "passwordcheck.min_password_length"`})
				}
				inquiry := func(allowed bool) fixture.Step {
					return fixture.Select("SELECT has_parameter_privilege('pc_acl_login','passwordcheck.min_password_length','SET') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{allowed}})
				}
				ra(fixture.Exec("SET passwordcheck.min_password_length=12", 0))
				ra(passwordCheckShow("12B"))
				run(passwordCheckShow("8B"))
				run(inquiry(false))
				for _, query := range []string{"SET passwordcheck.min_password_length=12", "SET passwordcheck.min_password_length='malformed'", "RESET passwordcheck.min_password_length", "SET passwordcheck.min_password_length=DEFAULT"} { run(denied(query)) }
				query := "SET passwordcheck.min_password_length=12"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil { t.Fatal("held setting preparation before grant", err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied(query))
				ra(fixture.Exec("GRANT SET ON PARAMETER passwordcheck.min_password_length TO pc_acl_login", 0))
				run(inquiry(true))
				owner := "postgres"
				if b.PG { owner = "stuntgres" }
				ra(fixture.Select("SELECT parname,paracl::text AS acl FROM pg_parameter_acl WHERE parname='passwordcheck.min_password_length'", []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, [][]any{{passwordCheckSetting, "{"+owner+"=sA/"+owner+",pc_acl_login=s/"+owner+"}"}}))
				var oid int64
				if err := admin.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname='passwordcheck.min_password_length'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 { t.Fatal("real parameter ACL identity", oid, err) }
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
				run(passwordCheckShow("12B"))
				ra(fixture.Exec("REVOKE SET ON PARAMETER passwordcheck.min_password_length FROM pc_acl_login", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied(query))
				run(denied("RESET passwordcheck.min_password_length"))
				run(passwordCheckShow("12B"))
				run(passwordCheckShort("ALTER ROLE CURRENT_USER PASSWORD 'Abcdef12'", 12))
				run(fixture.Exec("RESET ALL", 0))
				// RESET ALL restores the default without rechecking the SET ACL.
				run(passwordCheckShow("8B"))
				run(fixture.Exec("ALTER ROLE CURRENT_USER PASSWORD 'Abcdef12'", 0))
				ra(fixture.Exec("GRANT SET ON PARAMETER passwordcheck.min_password_length TO pc_acl_parent", 0))
				// The default ACL row can disappear after the last explicit grant
				// is revoked. Capture this new row, then change membership only.
				if err := admin.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname='passwordcheck.min_password_length'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 { t.Fatal("parent parameter ACL identity", oid, err) }
				ra(fixture.Exec("GRANT pc_acl_parent TO pc_acl_login WITH INHERIT TRUE", 0))
				run(inquiry(true))
				run(fixture.Exec("RESET passwordcheck.min_password_length", 0))
				run(passwordCheckShow("8B"))
				ra(fixture.Exec("GRANT pc_acl_parent TO pc_acl_login WITH INHERIT FALSE", 0))
				run(inquiry(false))
				run(denied(query))
				run(fixture.Exec("SET ROLE pc_acl_parent", 0))
				run(fixture.Exec(query, 0))
				run(passwordCheckShow("12B"))
				run(fixture.Exec("RESET ROLE", 0))
				run(denied("SET passwordcheck.min_password_length=DEFAULT"))
				run(passwordCheckShow("12B"))
				var after int64
				if err := admin.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname='passwordcheck.min_password_length'").Scan(&after); err != nil || after != oid { t.Fatal("ACL update changed object identity", oid, after, err) }
			})
		})
	}
}
