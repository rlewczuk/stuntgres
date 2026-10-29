package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

type roleDefaultsProvider func(context.Context, settings.Definition) (string, bool, error)

func (f roleDefaultsProvider) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	return f(ctx, d)
}

func roleDefaultsInstall(e *core.Engine, key access.RoleSettingKey, row []access.RoleSetting) *access.RoleRecord {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := e.RoleStore.Published.WithSettings(e.RoleStore.Published.Settings.WithRow(key, row))
	e.RoleStore.PublishLocked(next)
	e.RoleStore.AdvanceRevisionFrontierLocked(next.Revision)
	return next
}

func roleDefaultsStart(t *testing.T, e *core.Engine, start session.SessionStart, principals ...authorization.PrincipalRef) *session.Session {
	t.Helper()
	start.ID = ids.SessionID(testSessionID.Add(1))
	s, err := enginetest.OpenSession(t.Context(), e, start, principals...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestRoleSettingsStartupCapture(t *testing.T) {
	e := engine.New()
	login := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	database := e.Catalog.DatabaseAddress()
	scopes := []access.RoleSettingKey{{}, {Database: database}, {Role: login}, {Database: database, Role: login}}
	for i, key := range scopes {
		roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: []string{"global", "database", "login", "both"}[i]}})
	}
	roleDefaultsInstall(e, scopes[0], []access.RoleSetting{{Name: "application_name", Value: "global"}, {Name: "work_mem", Value: "1536kB"}, {Name: "capture.custom", Value: "custom"}})
	before := e.RoleStore.Published
	retained := before.Settings.Lookup(scopes[3])
	calls := make(map[string]int)
	callback := roleDefaultsProvider(func(ctx context.Context, d settings.Definition) (string, bool, error) {
		calls[d.Name]++
		if d.Name == "application_name" {
			if !e.Mu.TryLock() {
				return "", false, errors.New("provider ran under cluster mutex")
			}
			e.Mu.Unlock()
			roleDefaultsInstall(e, scopes[3], []access.RoleSetting{{Name: "application_name", Value: "later"}})
		}
		return "", false, nil
	})
	old := roleDefaultsStart(t, e, session.SessionStart{Defaults: callback})
	if old.State != session.Live || old.Settings.Session != old || old.Authorization.Session != old || old.Transactions.Session != old || !session.TokenAttached(old.SessionToken) {
		t.Fatal("startup attachment ownership lost")
	}
	if old.Settings.Defaults["application_name"] != "both" || old.Settings.DefaultSources["application_name"] != session.DefaultSourceDatabaseUser || old.Settings.DefaultTyped["work_mem"].Number != 1536 || old.Settings.DefaultSources["work_mem"] != session.DefaultSourceGlobal || !old.Settings.Placeholders["capture.custom"] {
		t.Fatal("startup did not retain coherent typed four-scope image", old.Settings.Defaults["application_name"], old.Settings.DefaultTyped["work_mem"])
	}
	if before == e.RoleStore.Published || !before.SameAuthority(e.RoleStore.Published) || before.Revision >= e.RoleStore.Published.Revision || retained[0].Value != "both" || &retained[0] == &e.RoleStore.Published.Settings.Lookup(scopes[3])[0] {
		t.Fatal("complete-record publication mutated retained backing")
	}
	for _, name := range append(append([]string(nil), e.Settings.Order()...), "role") {
		if calls[name] != 1 {
			t.Fatal("provider capture count", name, calls[name])
		}
	}
	if len(calls) != len(e.Settings.Order())+1 {
		t.Fatal("provider invoked for unregistered persisted placeholder", calls)
	}
	op, err := old.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	changed := "override"
	d := e.Settings.Definitions()["application_name"]
	if _, err = core.EngineApplySetting(e, op, old.Settings, d, &changed, false); err != nil {
		t.Fatal(err)
	}
	if _, err = core.EngineApplySetting(e, op, old.Settings, d, nil, false); err != nil {
		t.Fatal(err)
	}
	facts, err := settingsRuntimeForTest(t, e, old).SessionSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.Definition.Name == "application_name" && (f.Source != "database user" || f.Reset.Text != "both" || f.Value.Text != "both") {
			t.Fatal("RESET lost captured source/value", f)
		}
	}
	fresh := roleDefaultsStart(t, e, session.SessionStart{})
	if fresh.Settings.Defaults["application_name"] != "later" || old.Settings.Defaults["application_name"] != "both" {
		t.Fatal("physical replacement did not isolate reset images")
	}
	equal := roleDefaultsStart(t, e, session.SessionStart{Defaults: roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
		return "", d.Name == "application_name", nil
	})})
	if equal.Settings.Defaults["application_name"] != "" || !equal.Settings.ProviderDefaults["application_name"] {
		t.Fatal("equal provider response lost client precedence")
	}
	for _, f := range mustRoleSettingsFacts(t, e, equal) {
		if f.Definition.Name == "application_name" && f.Source != "client" {
			t.Fatal("equal provider source", f)
		}
	}
	private, err := e.StartRestoreSession(t.Context(), e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	if private.Settings.Defaults["application_name"] != "" || private.Settings.Placeholders["capture.custom"] {
		t.Fatal("private replay consumed committed defaults")
	}
	private.RequestEnd(session.CleanClose)
	if err := private.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func mustRoleSettingsFacts(t *testing.T, e *core.Engine, s *session.Session) []pgcatalog.SettingsFact {
	t.Helper()
	facts, err := settingsRuntimeForTest(t, e, s).SessionSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestRoleSettingsStartupNotices(t *testing.T) {
	for _, mode := range []string{"missing", "ineligible", "valid", "self"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			e.Mu.Lock()
			login := e.RoleStore.Domain.Principal(20000)
			target := e.RoleStore.Domain.Principal(20001)
			root, err := e.RoleStore.Published.Root.PutDescriptor(authorization.RoleDescriptor{Ref: login, Name: "capture_login", Login: true, Inherit: true, ConnectionLimit: -1})
			if err == nil {
				root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: target, Name: "capture_target", Inherit: true, ConnectionLimit: -1})
			}
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			if mode == "valid" { // Bootstrap is genuinely superuser; ordinary login is used for denial.
				login = e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
			}
			e.RoleStore.PublishLocked(e.RoleStore.Published.WithRoot(root))
			e.Mu.Unlock()
			name := "capture_target"
			if mode == "missing" {
				name = "capture_absent"
			}
			if mode == "self" {
				name, target = "capture_login", login
			}
			key := access.RoleSettingKey{Role: login}
			record := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "role", Value: name}, {Name: "application_name", Value: "login-only"}})
			if mode != "self" {
				roleDefaultsInstall(e, access.RoleSettingKey{Role: target}, []access.RoleSetting{{Name: "application_name", Value: "not-recursive"}})
			}
			var notices []sqlstate.Error
			s := roleDefaultsStart(t, e, session.SessionStart{NoticeHandler: func(n sqlstate.Error) {
				if !e.Mu.TryLock() {
					t.Error("notice callback under cluster mutex")
					return
				}
				e.Mu.Unlock()
				core.EngineSessionMuRef(e).Lock()
				for _, live := range core.EngineSessions(e) {
					if !live.Settings.Mu.TryLock() {
						t.Error("notice callback under settings mutex")
						continue
					}
					live.Settings.Mu.Unlock()
					if !live.Mu.TryLock() {
						t.Error("notice callback under session mutex")
						continue
					}
					live.Mu.Unlock()
				}
				core.EngineSessionMuRef(e).Unlock()
				notices = append(notices, n)
			}}, login)
			want := login
			valid := mode == "valid" || mode == "self"
			if valid {
				want = target
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			} else {
				code, message := "42501", `permission denied to set role "capture_target"`
				if mode == "missing" {
					code, message = "22023", `role "capture_absent" does not exist`
				}
				expected := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}}
				if !reflect.DeepEqual(notices, expected) {
					t.Fatal("startup notice fields", notices, expected)
				}
			}
			state := s.Authorization.State
			expected := authorization.RoleState{Authenticated: login, SessionUser: login, CurrentUser: want}
			if valid {
				expected.ResetRole, expected.ActiveRole = target, true
				expected.RoleText, expected.ResetRoleText = name, name
			}
			if state != expected || s.Settings.Defaults["application_name"] != "login-only" || record.Settings.Lookup(key)[0].Value != name {
				t.Fatal("special role identity/default capture changed", s.Authorization.State)
			}
			for _, f := range mustRoleSettingsFacts(t, e, s) {
				if f.Definition.Name == "role" {
					t.Fatal("special role leaked into pg_settings")
				}
			}
		})
	}
}
