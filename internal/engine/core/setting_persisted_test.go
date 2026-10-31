package core_test

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestRoleSettingsPersistedValidation(t *testing.T) {
	e := engine.New()
	s := roleDefaultsStart(t, e, session.SessionStart{})
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	originalDefaults, originalTyped := maps.Clone(s.Settings.Defaults), maps.Clone(s.Settings.DefaultTyped)
	current := "issuer"
	if _, err := core.EngineApplySetting(e, op, s.Settings, e.Settings.Definitions()["application_name"], &current, false); err != nil {
		t.Fatal(err)
	}
	values, typed, placeholders := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed), maps.Clone(s.Settings.Placeholders)
	actions := s.Settings.Savepoint()
	record := e.RoleStore.Published
	for _, tc := range []struct {
		name, input string
		code        string
	}{
		{"APPLICATION_NAME", "stored", ""}, {"capture.placeholder", "custom", ""},
		{"work_mem", "1536kB", ""}, {"capture_unknown", "x", "42704"},
		{"TIMEZONE", "UTC", ""}, {"DateStyle", "ISO, MDY", ""}, {"INTERVALSTYLE", "postgres", ""},
		{"work_mem", "not-memory", "22023"}, {"server_encoding", "UTF8", "55P02"},
		{"default_tablespace", "absent_capture_space", "22023"},
	} {
		canonical, stored, err := e.ValidatePersistedSetting(op, tc.name, &tc.input)
		if tc.code != "" {
			enginetest.State(t, err, tc.code)
		} else if err != nil || canonical != settings.DisplayName(settings.FoldName(tc.name)) || stored != tc.input {
			t.Fatal("pure extracted input changed", canonical, stored, err)
		}
		if tc.code == "" {
			resetName, _, resetErr := e.ValidatePersistedSetting(op, tc.name, nil)
			if resetErr != nil || resetName != canonical {
				t.Fatal("reset lost canonical catalog spelling", resetName, canonical, resetErr)
			}
		}
		if !reflect.DeepEqual(s.Settings.Values, values) || !reflect.DeepEqual(s.Settings.Typed, typed) || !reflect.DeepEqual(s.Settings.Placeholders, placeholders) || !reflect.DeepEqual(s.Settings.Savepoint(), actions) || !reflect.DeepEqual(s.Settings.Defaults, originalDefaults) || !reflect.DeepEqual(s.Settings.DefaultTyped, originalTyped) || e.RoleStore.Published != record {
			t.Fatal("persisted test mutated issuer or shared authority", tc.name)
		}
	}
	name, value, err := e.PersistedSettingCurrent(op, "APPLICATION_NAME")
	if err != nil || name != "application_name" || value != "issuer" {
		t.Fatal("FROM CURRENT did not read executing image", name, value, err)
	}
	name, value, err = e.PersistedSettingCurrent(op, "role")
	if err != nil || name != "role" || value != "none" {
		t.Fatal("FROM CURRENT role", name, value, err)
	}
	if _, _, err := e.ValidatePersistedSetting(op, "application_name", nil); err != nil {
		t.Fatal("pure reset", err)
	}
	if allowed, err := e.PersistedSettingResetAllowed(op, "server_encoding"); err != nil || allowed {
		t.Fatal("RESETALL admitted immutable parameter", allowed, err)
	}
	if allowed, err := e.PersistedSettingResetAllowed(op, "application_name"); err != nil || !allowed {
		t.Fatal("RESETALL refused mutable parameter", allowed, err)
	}
	identity := op.RoleState()
	for _, active := range []bool{false, true} {
		s.Mu.Lock()
		s.Authorization.State.ActiveRole = active
		s.Authorization.State.RoleText = ""
		if active {
			s.Authorization.State.RoleText = "postgres"
		}
		s.Mu.Unlock()
		captured := "none"
		if active {
			captured = "postgres"
		}
		_, value, err := e.PersistedSettingCurrent(op, "role")
		if err != nil || value != captured {
			t.Fatal("physical self role capture", value, err)
		}
		if _, stored, err := e.ValidatePersistedSetting(op, "role", &value); err != nil || stored != value {
			t.Fatal("plain role capture validation", stored, err)
		}
		state := op.RoleState()
		mark := s.Settings.Savepoint()
		op.EffectiveUser = &session.EffectiveUserInvocation{Owner: identity.CurrentUser}
		for _, restricted := range []bool{false, true} {
			contextName := "security-definer function"
			if restricted {
				op.Restricted = &session.RestrictedInvocation{Owner: identity.CurrentUser}
				contextName = "security-restricted operation"
			}
			for _, input := range []*string{nil, &value} {
				_, _, err := e.ValidatePersistedSetting(op, "role", input)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `cannot set parameter "role" within `+contextName {
					t.Fatal("persisted role validation lost source-independent restriction", err)
				}
				if op.RoleState() != state || e.RoleStore.Published != record || !reflect.DeepEqual(s.Settings.Savepoint(), mark) {
					t.Fatal("denied role validation changed physical authority")
				}
			}
			op.Restricted = nil
		}
		op.EffectiveUser = nil
	}
	s.Mu.Lock()
	s.Authorization.State = identity
	s.Mu.Unlock()
	if e.RoleStore.Published != record || !reflect.DeepEqual(s.Settings.Savepoint(), actions) {
		t.Fatal("restricted validation mutated authority or journal")
	}
}
