package core_test

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
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
		{"work_mem", "not-memory", "22023"}, {"server_encoding", "UTF8", "55P02"},
		{"default_tablespace", "absent_capture_space", "22023"},
	} {
		canonical, stored, err := e.ValidatePersistedSetting(op, tc.name, &tc.input)
		if tc.code != "" {
			enginetest.State(t, err, tc.code)
		} else if err != nil || canonical != settings.FoldName(tc.name) || stored != tc.input {
			t.Fatal("pure extracted input changed", canonical, stored, err)
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

func TestRoleSettingsCapturedInvocationImage(t *testing.T) {
	e := engine.New()
	login := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	roleDefaultsInstall(e, access.RoleSettingKey{Role: login}, []access.RoleSetting{{Name: "application_name", Value: "captured"}, {Name: "work_mem", Value: "1536kB"}})
	s := roleDefaultsStart(t, e, session.SessionStart{})
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	assign := func(store *session.SettingsStore, name, input string, action session.SettingAction) {
		t.Helper()
		d := e.Settings.Definitions()[name]
		value, err := d.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		store.Mu.Lock()
		defer store.Mu.Unlock()
		store.Touch(name, action)
		store.Values[name], store.Typed[name] = d.Format(value), value
	}
	s.Settings.Mu.Lock()
	s.Settings.BeginSettingsSaveLocked()
	s.Settings.Mu.Unlock()
	assign(s.Settings, "application_name", "outer", session.SettingSave)
	s.Settings.Mu.Lock()
	level := s.Settings.BeginSettingsSaveLocked()
	s.Mu.Lock()
	originalRole := s.Authorization.State
	originalRole.ActiveRole, originalRole.RoleText = true, "original-self-text"
	s.Authorization.State = originalRole
	s.Settings.TouchRole(originalRole, false)
	selectedRole := originalRole
	selectedRole.ActiveRole, selectedRole.RoleText = true, "accepted-self-text"
	s.Authorization.State = selectedRole
	s.Settings.TouchRole(selectedRole, true)
	s.Authorization.State.ActiveRole, s.Authorization.State.RoleText = false, ""
	s.Mu.Unlock()
	s.Settings.Mu.Unlock()
	assign(s.Settings, "application_name", "saved", session.SettingSave)
	assign(s.Settings, "work_mem", "2048kB", session.SettingSet)
	assign(s.Settings, "work_mem", "3072kB", session.SettingLocal)
	parent := s.Settings.Savepoint()
	parentDefaults, parentTyped, parentSources := maps.Clone(s.Settings.Defaults), maps.Clone(s.Settings.DefaultTyped), maps.Clone(s.Settings.DefaultSources)
	if level != 3 || len(parent.Actions) != 2 || !parent.RoleAction.Present {
		t.Fatal("invocation fixture lacks nested setting and role actions")
	}
	// Each payload component must distinguish the immutable action image even
	// when all principal refs are equal. A ref-only journal would collapse these.
	for _, component := range []string{"original-active", "original-text", "candidate-active", "candidate-text"} {
		original, candidate := originalRole, selectedRole
		switch component {
		case "original-active":
			original.ActiveRole = false
		case "original-text":
			original.RoleText = "other-original-text"
		case "candidate-active":
			candidate.ActiveRole = false
		case "candidate-text":
			candidate.RoleText = "other-candidate-text"
		}
		var witness session.SettingsStore
		witness.TouchRole(original, false)
		witness.TouchRole(candidate, true)
		if witness.RoleAction == parent.RoleAction {
			t.Fatal("role action discarded complete payload", component)
		}
	}
	op.Restricted = &session.RestrictedInvocation{Owner: login}
	detached := op.EffectiveSettings()
	if detached == s.Settings || detached.Session != s || !detached.Invocation || detached.NestLevel != 3 || detached.DefaultSources["application_name"] != session.DefaultSourceUser || detached.DefaultTyped["work_mem"].Number != 1536 || !reflect.DeepEqual(detached.Savepoint(), parent) {
		t.Fatal("detached invocation lost owner, nesting or typed reset image")
	}
	mark := op.CaptureInvocation()
	detached.DefaultSources["application_name"] = session.DefaultSourceGlobal
	detached.Defaults["application_name"] = "local-image"
	changedTyped := detached.DefaultTyped["work_mem"]
	changedTyped.Number = 4096
	detached.DefaultTyped["work_mem"] = changedTyped
	detached.Mu.Lock()
	detached.BeginSettingsSaveLocked()
	s.Mu.Lock()
	detached.TouchRole(originalRole, false)
	s.Mu.Unlock()
	detached.Mu.Unlock()
	assign(detached, "application_name", "changed", session.SettingSet)
	assign(detached, "work_mem", "4096kB", session.SettingSave)
	if reflect.DeepEqual(detached.Actions, parent.Actions) || detached.RoleAction == parent.RoleAction || reflect.DeepEqual(detached.Values, parent.Values) {
		t.Fatal("invocation mutation did not distinguish action and value state")
	}
	if s.Settings.NestLevel != 3 || s.Settings.DefaultSources["application_name"] != session.DefaultSourceUser || s.Settings.Defaults["application_name"] != "captured" {
		t.Fatal("detached defaults or nesting changed physical owner")
	}
	if !reflect.DeepEqual(s.Settings.Savepoint(), parent) || !reflect.DeepEqual(s.Settings.Defaults, parentDefaults) || !reflect.DeepEqual(s.Settings.DefaultTyped, parentTyped) || !reflect.DeepEqual(s.Settings.DefaultSources, parentSources) {
		t.Fatal("detached actions, values or reset metadata changed physical owner")
	}
	mark.Restore()
	restored := op.EffectiveSettings()
	if restored == detached || restored == s.Settings || restored.Session != s || !restored.Invocation || restored.NestLevel != 3 || restored.DefaultSources["application_name"] != session.DefaultSourceUser || restored.Defaults["application_name"] != "captured" || restored.DefaultTyped["work_mem"].Number != 1536 {
		t.Fatal("exception mark lost detached owner, nesting or reset metadata")
	}
	if !reflect.DeepEqual(restored.Savepoint(), parent) || !reflect.DeepEqual(restored.Defaults, parentDefaults) || !reflect.DeepEqual(restored.DefaultTyped, parentTyped) || !reflect.DeepEqual(restored.DefaultSources, parentSources) {
		t.Fatal("exception mark lost populated setting/role actions, values or reset image")
	}
	detached.DefaultSources["application_name"] = session.DefaultSourceDatabase
	detached.Defaults["application_name"] = "discarded-image"
	detached.Mu.Lock()
	detached.BeginSettingsSaveLocked()
	detached.Mu.Unlock()
	assign(detached, "application_name", "discarded", session.SettingLocal)
	if restored.DefaultSources["application_name"] != session.DefaultSourceUser || restored.Defaults["application_name"] != "captured" || restored.NestLevel != 3 {
		t.Fatal("checkpoint shares mutable backing with discarded invocation")
	}
	if err := detached.EndSession(s.Token(), session.CleanClose); err != nil {
		t.Fatal(err)
	}
	if !detached.Ended || detached.DefaultSources != nil || detached.DefaultTyped != nil || detached.Defaults != nil || detached.Values != nil || detached.Typed != nil || detached.Actions != nil || detached.RoleAction.Present || detached.NestLevel != 0 {
		t.Fatal("ended detached owner retains reset, value or action state")
	}
	if !reflect.DeepEqual(restored.Savepoint(), parent) || !reflect.DeepEqual(restored.Defaults, parentDefaults) || !reflect.DeepEqual(restored.DefaultTyped, parentTyped) || !reflect.DeepEqual(restored.DefaultSources, parentSources) {
		t.Fatal("checkpoint shares action, value or reset backing with discarded invocation")
	}
	restored.DefaultSources["application_name"] = session.DefaultSourceGlobal
	restored.Defaults["application_name"] = "restored-local"
	restored.Mu.Lock()
	restored.BeginSettingsSaveLocked()
	s.Mu.Lock()
	restored.TouchRole(originalRole, false)
	s.Mu.Unlock()
	restored.Mu.Unlock()
	assign(restored, "application_name", "restored-value", session.SettingSet)
	if s.Settings.DefaultSources["application_name"] != session.DefaultSourceUser || s.Settings.Defaults["application_name"] != "captured" || s.Settings.NestLevel != 3 {
		t.Fatal("restored invocation shares mutable backing with physical owner")
	}
	if !reflect.DeepEqual(s.Settings.Savepoint(), parent) || !reflect.DeepEqual(s.Settings.Defaults, parentDefaults) || !reflect.DeepEqual(s.Settings.DefaultTyped, parentTyped) || !reflect.DeepEqual(s.Settings.DefaultSources, parentSources) {
		t.Fatal("restored invocation shares action, value or reset backing with physical owner")
	}
	if err := restored.EndSession(s.Token(), session.CleanClose); err != nil {
		t.Fatal(err)
	}
	if !restored.Ended || restored.DefaultSources != nil || restored.DefaultTyped != nil || restored.Defaults != nil || restored.Values != nil || restored.Typed != nil || restored.Actions != nil || restored.RoleAction.Present || restored.NestLevel != 0 {
		t.Fatal("ended restored owner retains reset, value or action state")
	}
	if !reflect.DeepEqual(s.Settings.Savepoint(), parent) || !reflect.DeepEqual(s.Settings.Defaults, parentDefaults) || !reflect.DeepEqual(s.Settings.DefaultTyped, parentTyped) || !reflect.DeepEqual(s.Settings.DefaultSources, parentSources) {
		t.Fatal("ending detached owner changed physical owner")
	}
	op.Restricted = nil
	s.RequestEnd(session.CleanClose)
	op.Done()
	if err := s.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.Settings.DefaultSources != nil || s.Settings.DefaultTyped != nil || s.Settings.Defaults != nil {
		t.Fatal("ended physical owner retains reset metadata")
	}
}
