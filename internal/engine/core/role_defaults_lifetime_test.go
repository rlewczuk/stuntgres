package core_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestRoleSettingsStartupUnwind(t *testing.T) {
	for _, mode := range []string{"provider-error", "cancel", "final-login-fence"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			login := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
			key := access.RoleSettingKey{Role: login}
			record := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "retained"}, {Name: "role", Value: "postgres"}})
			row := record.Settings.Lookup(key)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("captured provider failed")
			seenRole := false
			provider := roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
				if mode == "provider-error" && d.Name == "application_name" {
					return "", false, failure
				}
				if mode == "cancel" && d.Name == "application_name" {
					cancel()
				}
				if d.Name == "role" {
					seenRole = true
					if mode == "final-login-fence" {
						e.Mu.Lock()
						descriptor, _ := e.RoleStore.Published.Root.Read(login).Descriptor(login)
						descriptor.Login = false
						next, err := e.RoleStore.Published.Root.PutDescriptor(descriptor)
						if err != nil {
							e.Mu.Unlock()
							return "", false, err
						}
						e.RoleStore.PublishLocked(e.RoleStore.Published.WithRoot(next))
						e.Mu.Unlock()
					}
				}
				return "", false, nil
			})
			s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: provider})
			if s != nil || err == nil {
				t.Fatal("failed startup published a session", s, err)
			}
			if mode == "provider-error" && !errors.Is(err, failure) {
				t.Fatal("provider error changed", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("startup cancellation changed", err)
			}
			if mode == "final-login-fence" && !seenRole {
				t.Fatal("final publication fence not reached")
			}
			core.EngineSessionMuRef(e).Lock()
			count := len(core.EngineSessions(e))
			core.EngineSessionMuRef(e).Unlock()
			if count != 0 || len(e.Locks.Inspect().Sessions) != 0 {
				t.Fatal("failed initialization leaked live/lock registration", count, e.Locks.Inspect())
			}
			if record.Settings != e.RoleStore.Published.Settings || &row[0] != &e.RoleStore.Published.Settings.Lookup(key)[0] || row[0].Value != "retained" {
				t.Fatal("failed startup changed committed defaults/backing")
			}
		})
	}
}

func TestRoleSettingsStartupNativeReplacement(t *testing.T) {
	e := engine.New()
	login := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	key := access.RoleSettingKey{Role: login}
	record := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "destination"}, {Name: "search_path", Value: "missing_schema"}, {Name: "role", Value: "missing_replay_role"}})
	retained := record.Settings.Lookup(key)
	s := roleDefaultsStart(t, e, session.SessionStart{Defaults: roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
		if d.Name == "role" {
			return "postgres", true, nil
		}
		return "", false, nil
	})})
	identity := s.Authorization.State
	if !identity.ActiveRole || identity.CurrentUser != login || identity.ResetRole != login || identity.RoleText != "postgres" || identity.ResetRoleText != "postgres" {
		t.Fatal("provider self role lost captured reset state", identity)
	}
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if e.RoleStore.Published.Settings != record.Settings || s.Settings.Defaults["application_name"] != "destination" || s.Authorization.State != identity {
		t.Fatal("Reset discarded destination default ownership")
	}
	if err := engine.Restore(t.Context(), e, strings.NewReader("CREATE TABLE capture_restore(v integer);")); err != nil {
		t.Fatal(err)
	}
	if e.RoleStore.Published.Settings != record.Settings || &retained[0] != &e.RoleStore.Published.Settings.Lookup(key)[0] || retained[0].Value != "destination" || s.Authorization.State != identity {
		t.Fatal("Restore imported/rebuilt destination settings")
	}
	fresh := roleDefaultsStart(t, e, session.SessionStart{})
	if fresh.Settings.Defaults["application_name"] != "destination" || fresh.Settings.Defaults["search_path"] != "missing_schema" || fresh.Authorization.State != (authorization.RoleState{Authenticated: login, SessionUser: login, CurrentUser: login}) {
		t.Fatal("replacement did not retain destination startup capture")
	}
	core.EngineSessionMuRef(e).Lock()
	count := len(core.EngineSessions(e))
	core.EngineSessionMuRef(e).Unlock()
	if count != 2 || len(e.Locks.Inspect().Sessions) != 2 {
		t.Fatal("private Restore session leaked attachments", count)
	}
}
