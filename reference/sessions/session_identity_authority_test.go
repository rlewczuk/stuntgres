package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentitySessionAuthorityAndArgumentPrecedence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_member_a", "identity_member_b", "identity_member_c"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		b := struct {
			name   string
			db     *sql.DB
			record *fixture.Recorder
			native bool
		}{backend.Name, backend.DB, backend.Recorder, !backend.PG}
		t.Run(b.name, func(t *testing.T) {
			t.Cleanup(func() {
				exec := b.db.ExecContext
				if !b.native {
					exec = fixture.Admin.ExecContext
				}
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS identity_hidden CASCADE"} {
					if _, err := exec(p.Ctx, q); err != nil {
						t.Error("identity authority cleanup", q, err)
					}
				}
			})
			conn, err := b.db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.Prepared, s.Query)
				fixture.RunStep(t, p.Ctx, conn, b.record, s)
			}
			for _, q := range []string{"CREATE USER identity_member_a", "CREATE ROLE identity_member_b", "CREATE ROLE identity_member_c", "CREATE SCHEMA identity_hidden"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("SET ROLE identity_member_a", 0))
			run(fixture.Exec("SET ROLE identity_member_b", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_member_a", 0))
			run(fixture.Exec("SET ROLE identity_member_a", 0))
			fixture.WithPublicSchemaOwner(t, p, backend, "identity_member_a", func() { run(fixture.Exec("CREATE TABLE identity_child(id INT)", 0)) })
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SET ROLE identity_member_b", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "identity_member_b"`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				s = fixture.ImplicitError("SET ROLE absent_identity_role", fixture.Diagnostic{Code: "22023", Message: `role "absent_identity_role" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				s = fixture.ImplicitError("SELECT identity_hidden.current_database()", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema identity_hidden", Position: "8"})
				s.Prepared = prepared
				run(s)
				for _, tc := range []struct{ query, position string }{
					{"SELECT missing_identity_schema.current_database(missing_column) FROM identity_child", "49"},
					{"SELECT identity_hidden.current_database(missing_column) FROM identity_child", "41"},
				} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: tc.position})
					s.Prepared = prepared
					run(s)
					s.SelectRows = true
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
			}
			run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("DROP TABLE identity_child", 0))
			run(fixture.Exec("DROP SCHEMA identity_hidden", 0))
			run(fixture.Exec("DROP ROLE identity_member_a,identity_member_b,identity_member_c", 0))
		})
	}
}
