package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSessionIdentitySessionAuthorityAndArgumentPrecedence(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_member_a", "identity_member_b", "identity_member_c"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		b := struct {
			name   string
			db     *sql.DB
			record *referenceRecorder
			native bool
		}{backend.name, backend.db, backend.recorder, !backend.pg}
		t.Run(b.name, func(t *testing.T) {
			t.Cleanup(func() {
				exec := b.db.ExecContext
				if !b.native {
					exec = referenceAdmin.ExecContext
				}
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS identity_hidden CASCADE"} {
					if _, err := exec(p.ctx, q); err != nil {
						t.Error("identity authority cleanup", q, err)
					}
				}
			})
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				referenceRunStep(t, p.ctx, conn, b.record, s)
			}
			for _, q := range []string{"CREATE USER identity_member_a", "CREATE ROLE identity_member_b", "CREATE ROLE identity_member_c", "CREATE SCHEMA identity_hidden"} {
				run(referenceExec(q, 0))
			}
			if !b.native {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_member_a", 0))
			}
			run(referenceExec("SET ROLE identity_member_a", 0))
			run(referenceExec("SET ROLE identity_member_b", 0))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET SESSION AUTHORIZATION identity_member_a", 0))
			run(referenceExec("SET ROLE identity_member_a", 0))
			run(referenceExec("CREATE TABLE identity_child(id INT)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SET ROLE identity_member_b", referenceDiagnostic{Code: "42501", Message: `permission denied to set role "identity_member_b"`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				s = referenceImplicitError("SET ROLE absent_identity_role", referenceDiagnostic{Code: "22023", Message: `role "absent_identity_role" does not exist`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				s = referenceImplicitError("SELECT identity_hidden.current_database()", referenceDiagnostic{Code: "42501", Message: "permission denied for schema identity_hidden", Position: "8"})
				s.prepared = prepared
				run(s)
				for _, tc := range []struct{ query, position string }{
					{"SELECT missing_identity_schema.current_database(missing_column) FROM identity_child", "49"},
					{"SELECT identity_hidden.current_database(missing_column) FROM identity_child", "41"},
				} {
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: tc.position})
					s.prepared = prepared
					run(s)
					s.selectRows = true
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					run(s)
				}
			}
			run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if !b.native {
				run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_member_a", 0))
			}
			run(referenceExec("DROP TABLE identity_child", 0))
			run(referenceExec("DROP SCHEMA identity_hidden", 0))
			run(referenceExec("DROP ROLE identity_member_a,identity_member_b,identity_member_c", 0))
		})
	}
}
