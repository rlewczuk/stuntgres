package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestDatabaseSettingsStartup(t *testing.T) {
	t.Run("canonical_catalog_names", func(t *testing.T) {
		e := engine.New()
		roleDefaultsInstall(e, access.RoleSettingKey{}, []access.RoleSetting{{Name: "timezone", Value: "UTC"}})
		roleDefaultsInstall(e, access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}, []access.RoleSetting{{Name: "TimeZone", Value: "GMT"}})
		s := roleDefaultsStart(t, e, session.SessionStart{})
		if s.Settings.Defaults["timezone"] != "GMT" || s.Settings.DefaultSources["timezone"] != session.DefaultSourceDatabase {
			t.Fatal("canonical catalog name lost folded startup precedence")
		}
		client := roleDefaultsStart(t, e, session.SessionStart{Defaults: roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
			return "UTC", d.Name == "timezone", nil
		})})
		if client.Settings.Defaults["timezone"] != "UTC" || !client.Settings.ProviderDefaults["timezone"] {
			t.Fatal("canonical catalog name bypassed client default")
		}
	})
	t.Run("committed_capture_before_callback", func(t *testing.T) {
		e := engine.New()
		key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
		before := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "captured"}})
		row := before.Settings.Lookup(key)
		calls := make(map[string]int)
		old := roleDefaultsStart(t, e, session.SessionStart{Defaults: roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
			calls[d.Name]++
			if d.Name == "application_name" {
				if !e.Mu.TryLock() {
					return "", false, errors.New("provider under publication mutex")
				}
				e.Mu.Unlock()
				roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "later"}})
			}
			return "", false, nil
		})})
		if old.Settings.Defaults["application_name"] != "captured" || old.Settings.DefaultSources["application_name"] != session.DefaultSourceDatabase || row[0].Value != "captured" || &row[0] == &e.RoleStore.Published.Settings.Lookup(key)[0] {
			t.Fatal("startup borrowed live row instead of committed image")
		}
		for _, name := range append(append([]string(nil), e.Settings.Order()...), "role") {
			if calls[name] != 1 {
				t.Fatal("provider not once per definition", name, calls[name])
			}
		}
		op, err := old.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		value := "override"
		d := e.Settings.Definitions()["application_name"]
		if _, err = core.EngineApplySetting(e, op, old.Settings, d, &value, false); err != nil {
			op.Done()
			t.Fatal(err)
		}
		if _, err = core.EngineApplySetting(e, op, old.Settings, d, nil, false); err != nil {
			op.Done()
			t.Fatal(err)
		}
		op.Done()
		for _, fact := range mustRoleSettingsFacts(t, e, old) {
			if fact.Definition.Name == "application_name" && (fact.Value.Text != "captured" || fact.Reset.Text != "captured" || fact.Source != "database") {
				t.Fatal("reset image was swept", fact)
			}
		}
		fresh := roleDefaultsStart(t, e, session.SessionStart{})
		if fresh.Settings.Defaults["application_name"] != "later" || old.Settings.Defaults["application_name"] != "captured" {
			t.Fatal("physical default lifetime")
		}
		client := roleDefaultsStart(t, e, session.SessionStart{Defaults: roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
			return "client", d.Name == "application_name", nil
		})})
		if client.Settings.Defaults["application_name"] != "client" || !client.Settings.ProviderDefaults["application_name"] {
			t.Fatal("client precedence lost")
		}
		private, err := e.StartRestoreSession(t.Context(), e.RoleStore.Domain.Principal(10))
		if err != nil {
			t.Fatal(err)
		}
		if private.Settings.Defaults["application_name"] != "" {
			t.Fatal("private replay consumed database defaults")
		}
		private.RequestEnd(session.CleanClose)
		if err := private.WaitEnd(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
	for _, mode := range []string{"error", "cancel"} {
		t.Run("unwind_"+mode, func(t *testing.T) {
			e := engine.New()
			key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
			record := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "retained"}})
			row := record.Settings.Lookup(key)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("database startup provider failed")
			provider := roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
				if d.Name == "application_name" {
					if mode == "error" {
						return "", false, failure
					}
					cancel()
				}
				return "", false, nil
			})
			s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: provider})
			want := failure
			if mode == "cancel" {
				want = context.Canceled
			}
			if s != nil || !errors.Is(err, want) {
				t.Fatal("startup published after failure", s, err)
			}
			core.EngineSessionMuRef(e).Lock()
			count := len(core.EngineSessions(e))
			core.EngineSessionMuRef(e).Unlock()
			if count != 0 || len(e.Locks.Inspect().Sessions) != 0 || e.RoleStore.Published != record || &row[0] != &record.Settings.Lookup(key)[0] || row[0].Value != "retained" {
				t.Fatal("startup unwind leaked attachment or changed backing")
			}
		})
	}
}
