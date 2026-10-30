package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// This shared scenario also owns the former PostgreSQL-only
// TestReferenceNoticesPermission witness; errors are never filtered notices.
func TestReferenceClientMinMessagesAuthority(t *testing.T) {
	const role = "notices_no_create"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			s := cmmOpen(t, p, backend.PG)
			s.run(fixture.Exec("CREATE ROLE "+role+" NOLOGIN", 0))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE ALL ON SCHEMA public FROM " + role, "DROP ROLE " + role} {
					if _, err := s.conn.ExecContext(ctx, query); err != nil {
						t.Error("authority cleanup", query, err)
					}
				}
			})
			s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
			s.run(fixture.Exec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
			s.run(fixture.Exec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
			s.run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			s.run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
			for _, prepared := range []bool{false, true} {
				for _, value := range []string{"notice", "warning"} {
					s.set(value, prepared)
					s.read(value, prepared, true)
				}
				reset := fixture.Exec("RESET client_min_messages", 0)
				reset.Prepared = prepared
				s.run(reset)
				s.read("notice", prepared, false)
			}
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
			for _, value := range []string{"notice", "warning", "error"} {
				s.set(value, false)
				s.read(value, false, true)
				for _, mode := range []string{"exec", "prepared-exec"} {
					before := len(s.notices)
					err := noticesCommand(t, p.Ctx, s.conn, s.record, &s.notices, mode, "CREATE TABLE IF NOT EXISTS notice_rows(other INT)")
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema public", Position: "28"})
					noticesWitness(t, backend.PG, s.notices[before:], wantNotice, false)
					s.read(value, mode == "prepared-exec", true)
				}
			}
			s.run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			noticesRows(t, p.Ctx, s.conn, s.record)
		})
	}
}
