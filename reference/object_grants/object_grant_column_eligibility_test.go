package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantColumnEligibility(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE og_elig_first START 101", 0))
		run(fixture.Exec("CREATE SEQUENCE og_elig_late START 201", 0))
		owner, _ := fixture.RoleGrantEnvironment(t, p, c)
		last := fixture.Select("SELECT last_value FROM og_elig_first", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(101)}})
		// Prepare once as owner before either role has rights. Every execution
		// below is as the recipient, across failed and successful ACL changes.
		stmt, err := c.PrepareContext(p.Ctx, last.Query)
		if err != nil {
			t.Fatal(last.Query, err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: last.Query}
		other := func(allowed bool) {
			fixture.RoleGrantActor(run, "og_object_other", func() {
				s := last
				if !allowed {
					s = fixture.ImplicitError(last.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence og_elig_first"})
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
		}
		reader := func(body func()) { fixture.RoleGrantActor(run, "og_object_reader", body) }
		columnError := func(query, column, relation string) fixture.Step {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: `permission denied for column "` + column + `" of relation "` + relation + `"`})
			s.ErrorStage = "execute"
			return s
		}
		rawColumn := func(relation string, rows [][]any) {
			call := "pg_get_acl(1259,'" + relation + "'::regclass,1)"
			s := fixture.Select("SELECT unnest("+call+") AS acl", []string{"acl"}, []string{"ACLITEM"}, rows)
			s.Bag = true
			run(s)
			if len(rows) == 0 {
				// Empty expansion alone does not distinguish NULL from an empty ACL.
				run(fixture.Select("SELECT "+call+" AS acl", []string{"acl"}, []string{"_ACLITEM"}, [][]any{{nil}}))
			}
		}
		firstACL := func(delegated bool) {
			rows := [][]any{{"og_object_reader=r*/" + owner}}
			if delegated {
				rows = append(rows, []any{"og_object_other=r/og_object_reader"})
			}
			rawColumn("og_elig_first", rows)
			rawColumn("og_elig_late", nil)
		}
		run(fixture.Exec("GRANT SELECT(last_value) ON TABLE og_elig_first TO og_object_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_elig_late TO og_object_reader", 0))
		other(false)
		reader(func() {
			run(columnError("GRANT SELECT(last_value) ON TABLE og_elig_late TO og_object_other", "last_value", "og_elig_late"))
			run(columnError("REVOKE SELECT(last_value) ON TABLE og_elig_late FROM og_object_other", "last_value", "og_elig_late"))
			run(columnError("GRANT SELECT(last_value) ON TABLE og_elig_first,og_elig_late TO og_object_other", "last_value", "og_elig_late"))
		})
		firstACL(false)
		other(false)
		reader(func() { run(fixture.Exec("GRANT SELECT(last_value) ON TABLE og_elig_first TO og_object_other", 0)) })
		other(true)
		reader(func() {
			run(columnError("REVOKE SELECT(last_value) ON TABLE og_elig_first,og_elig_late FROM og_object_other", "last_value", "og_elig_late"))
		})
		firstACL(true)
		other(true)

		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, recorder, s)
		}
		// The success-only notice helper cannot exercise warning-before-error.
		// Use the same public step runner and assert quiet description separately.
		noticeError := func(s fixture.Step, want ...fixture.Diagnostic) {
			notices = nil
			quiet, err := nc.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(s.Query, "Prepare", err)
			}
			fixture.RegtypeAssertNotices(t, notices, nil)
			if err := quiet.Close(); err != nil {
				t.Fatal("close quiet description", err)
			}
			notices = nil
			nrun(s)
			fixture.RegtypeAssertNotices(t, notices, want)
		}
		warning := func(code, message string) fixture.Diagnostic {
			return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}
		}
		nrun(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		noticeError(columnError("GRANT SELECT,SELECT(last_value) ON TABLE og_elig_late TO og_object_other", "last_value", "og_elig_late"),
			warning("01007", `no privileges were granted for "og_elig_late"`))
		// Whole SELECT revocation visits implicit system columns first; the
		// released first failing attribute is tableoid, not explicit last_value.
		noticeError(columnError("REVOKE SELECT,SELECT(last_value) ON TABLE og_elig_late FROM og_object_other", "tableoid", "og_elig_late"),
			warning("01006", `no privileges could be revoked for "og_elig_late"`))
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		firstACL(true)
		other(true)

		// UPDATE is column-applicable preliminary authority even on a sequence
		// whose explicit column privileges are SELECT-only. No grant option:
		// warn, do not error, and do not manufacture a recipient column grant.
		run(fixture.Exec("REVOKE USAGE ON SEQUENCE og_elig_late FROM og_object_reader", 0))
		run(fixture.Exec("GRANT UPDATE ON SEQUENCE og_elig_late TO og_object_reader", 0))
		nrun(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT(last_value) ON TABLE og_elig_late TO og_object_other",
			warning("01007", `no privileges were granted for column "last_value" of relation "og_elig_late"`))
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE SELECT(last_value) ON TABLE og_elig_late FROM og_object_other",
			warning("01006", `no privileges could be revoked for column "last_value" of relation "og_elig_late"`))
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		rawColumn("og_elig_late", nil)
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM og_elig_late", "sequence", "og_elig_late")
		})

		// The ordinary-table seam uses the same column eligibility mask: DELETE
		// alone is not preliminary authority for SELECT(x), even in mixed syntax.
		run(fixture.Exec("CREATE TABLE og_elig_table(x INTEGER)", 0))
		run(fixture.Exec("INSERT INTO og_elig_table VALUES(7)", 1))
		run(fixture.Exec("GRANT DELETE ON TABLE og_elig_table TO og_object_reader", 0))
		nrun(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		noticeError(columnError("GRANT SELECT(x) ON TABLE og_elig_table TO og_object_other", "x", "og_elig_table"))
		noticeError(columnError("REVOKE SELECT(x) ON TABLE og_elig_table FROM og_object_other", "x", "og_elig_table"))
		noticeError(columnError("GRANT DELETE,SELECT(x) ON TABLE og_elig_table TO og_object_other", "x", "og_elig_table"),
			warning("01007", `no privileges were granted for "og_elig_table"`))
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		rawColumn("og_elig_table", nil)
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_elig_table", "table", "og_elig_table")
		})
		run(fixture.Select("SELECT x FROM og_elig_table", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// No generator calls while USAGE/UPDATE is present: ACL work must leave
		// both independently specified initial values and is_called untouched.
		run(fixture.Select("SELECT last_value,is_called FROM og_elig_first", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(101), false}}))
		run(fixture.Select("SELECT last_value,is_called FROM og_elig_late", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(201), false}}))
	})
}
