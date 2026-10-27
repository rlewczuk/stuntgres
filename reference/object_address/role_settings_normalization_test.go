package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsSpecialRoleNameNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, accepted, suffix string
	}{
		{"ascii", "rs_f2_ASCII_" + strings.Repeat("x", 51), "_suffix"},
		// 62 bytes fit; the following three-byte euro sign must be omitted whole.
		{"utf8", "rs_f2_UTF8_" + strings.Repeat("é", 25) + "x", "€tail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const loginName = "rs_f2_login"
			const renamed = "rs_f2_renamed"
			original := tc.accepted + tc.suffix
			rsActionCases(t, []string{loginName, tc.accepted, renamed}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
				run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
				run(fixture.Exec("CREATE ROLE "+loginName+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
				var assignmentNotices []fixture.Diagnostic
				_, issuer, recorder := fixture.NoticeReferencePool(t, p, b.PG, &assignmentNotices)
				noticeBackend := b
				noticeBackend.Recorder = recorder
				assign := func() {
					assignmentNotices = nil
					rsActionRun(t, p, noticeBackend, issuer, route, fixture.Exec("ALTER ROLE "+loginName+" SET role='"+original+"'", 0))
				}
				clipping := fixture.Diagnostic{
					Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622",
					Message: `identifier "` + original + `" will be truncated to "` + tc.accepted + `"`,
				}
				// Released guc.c truncates before check_role; GUCArrayAdd stores the
				// original string even when PGC_S_TEST notices a missing clipped role.
				assign()
				missing := fixture.Diagnostic{
					Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42704",
					Message: `role "` + tc.accepted + `" does not exist`,
				}
				if want := []fixture.Diagnostic{clipping, missing}; !reflect.DeepEqual(assignmentNotices, want) {
					t.Fatalf("missing-target assignment notices = %#v, want %#v", assignmentNotices, want)
				}
				run(rsActionConfig(loginName, "{role="+original+"}"))
				// Quoting preserves the mixed-case accepted text; no CREATE-time
				// identifier clipping can accidentally create the expected target.
				run(fixture.Exec(`CREATE ROLE "`+tc.accepted+`"`, 0))
				run(fixture.Exec(`GRANT "`+tc.accepted+`" TO `+loginName+` WITH SET TRUE`, 0))
				assign()
				if want := []fixture.Diagnostic{clipping}; !reflect.DeepEqual(assignmentNotices, want) {
					t.Fatalf("valid-target assignment notices = %#v, want %#v", assignmentNotices, want)
				}
				run(rsActionConfig(loginName, "{role="+original+"}"))
				var startupNotices []fixture.Diagnostic
				pool := rsStartupPool(t, p, b, loginName, "", &startupNotices)
				login, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer login.Close()
				if want := []fixture.Diagnostic{clipping}; !reflect.DeepEqual(startupNotices, want) {
					t.Fatalf("startup notices = %#v, want %#v", startupNotices, want)
				}
				self := func(s fixture.Step) { rsActionRun(t, p, b, login, route, rsStartupStep(s)) }
				identity := func(current, role string) {
					self(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{loginName, current}}))
					self(fixture.Select("SHOW role", []string{"role"}, []string{"TEXT"}, [][]any{{role}}))
					self(fixture.Select("SELECT current_setting('role')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{role}}))
				}
				identity(tc.accepted, tc.accepted)
				// Neither replacing the persisted text nor renaming the target may
				// replace this connection's captured accepted text/reference pair.
				run(fixture.Exec("ALTER ROLE "+loginName+" SET role='none'", 0))
				run(rsActionConfig(loginName, "{role=none}"))
				run(fixture.Exec(`ALTER ROLE "`+tc.accepted+`" RENAME TO `+renamed, 0))
				identity(renamed, tc.accepted)
				self(fixture.Exec("SET ROLE NONE", 0))
				identity(loginName, "none")
				self(fixture.Exec("RESET ROLE", 0))
				identity(renamed, tc.accepted)
			})
		})
	}
}
