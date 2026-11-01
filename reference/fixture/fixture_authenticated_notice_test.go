package fixture

import (
	"reflect"
	"testing"
)

func TestReferenceFixtureAuthenticatedNoticeOwnership(t *testing.T) {
	RoleReferenceCleanup(t, []string{"fixture_notice_login"})
	SchemaReferenceCleanup(t, "fixture_notice_schema")
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var bootstrapNotices, loginNotices, handlerNotices []Diagnostic
			_, owner, ownerRecord := NoticeReferencePool(t, p, b.PG, &bootstrapNotices)
			RunStep(t, p.Ctx, owner, ownerRecord, Exec("CREATE ROLE fixture_notice_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
			RunStep(t, p.Ctx, owner, ownerRecord, Exec("ALTER ROLE fixture_notice_login SET application_name='captured-notice-login'", 0))
			RunStep(t, p.Ctx, owner, ownerRecord, Exec("CREATE SCHEMA fixture_notice_schema AUTHORIZATION fixture_notice_login", 0))
			_, login, loginRecord := NoticeReferencePool(t, p, b.PG, &loginNotices, NoticeReferenceOption{AuthenticatedRole: "fixture_notice_login", Handler: func(d Diagnostic) { handlerNotices = append(handlerNotices, d) }})
			if SettingsPhysical(t, owner) == SettingsPhysical(t, login) {
				t.Fatal("login reused bootstrap physical owner")
			}
			RunStep(t, p.Ctx, login, loginRecord, Select("SELECT current_user,session_user,current_setting('application_name')", []string{"current_user", "session_user", "current_setting"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"fixture_notice_login", "fixture_notice_login", "captured-notice-login"}}))
			RunStep(t, p.Ctx, login, loginRecord, Exec("CREATE TABLE fixture_notice_schema.notice_owned(v int)", 0))
			for _, prepared := range []bool{false, true} {
				s := Exec("CREATE TABLE IF NOT EXISTS fixture_notice_schema.notice_owned(v text)", 0)
				s.Prepared = prepared
				RunStep(t, p.Ctx, login, loginRecord, s)
				want := []Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_owned" already exists, skipping`}}
				if !reflect.DeepEqual(loginNotices, want) || !reflect.DeepEqual(handlerNotices, want) || len(bootstrapNotices) != 0 {
					t.Fatal("authenticated notice ownership", loginNotices, handlerNotices, bootstrapNotices)
				}
				loginNotices, handlerNotices = nil, nil
			}
			RunStep(t, p.Ctx, owner, ownerRecord, Select("SELECT current_user=session_user AS bootstrap_identity,current_user='fixture_notice_login' AS changed", []string{"bootstrap_identity", "changed"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
			RunStep(t, p.Ctx, owner, ownerRecord, Select("SELECT v FROM fixture_notice_schema.notice_owned", []string{"v"}, []string{"INT4"}, nil))
		})
	}
}
