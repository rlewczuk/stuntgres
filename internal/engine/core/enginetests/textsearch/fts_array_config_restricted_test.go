package textsearch_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestFTSArrayConfigRestrictedSettings(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.array_cfg_restricted (COPY=pg_catalog.simple)")
	enginetest.SessionSQL(t, s, "SET default_text_search_config='english'")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "array_cfg_restricted"})
	if address.ObjectID == 0 {
		t.Fatal("missing real custom configuration descriptor")
	}
	english := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "english"})
	if english.ObjectID == 0 {
		t.Fatal("missing real English configuration descriptor")
	}
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	result, ok := e.CatalogRead().LookupType(sqltype.Regconfig)
	if !ok {
		t.Fatal("missing real REGCONFIG descriptor")
	}
	runtime := core.HostOf(scope).TextSearch()
	check := func(want uint32) {
		t.Helper()
		v, err := runtime.CurrentTextSearchConfig(t.Context(), result)
		if err != nil || v.Type() != result || v.IsNull() {
			t.Fatal("actual operation configuration", v, err)
		}
		oid, err := v.RegTSOID()
		if err != nil || oid != want {
			t.Fatal("physical settings shadowed restricted invocation", oid, want, err)
		}
		s.Settings.Mu.Lock()
		physical := s.Settings.Value("default_text_search_config")
		s.Settings.Mu.Unlock()
		if physical != "pg_catalog.english" {
			t.Fatal("restricted lookup mutated physical settings", physical)
		}
	}
	assign := func(store *session.SettingsStore, name string) {
		t.Helper()
		definition := e.Settings.Definitions()["default_text_search_config"]
		value, err := definition.Parse(name)
		if err != nil {
			t.Fatal(err)
		}
		store.Mu.Lock()
		store.Touch("default_text_search_config", session.SettingSet)
		store.Values["default_text_search_config"], store.Typed["default_text_search_config"] = definition.Format(value), value
		store.Mu.Unlock()
	}
	check(uint32(english.ObjectID))
	owner := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	parent := &session.RestrictedInvocation{Owner: owner}
	op.Restricted = parent
	parentSettings := op.EffectiveSettings()
	if parentSettings == s.Settings {
		t.Fatal("parent settings were not detached")
	}
	assign(parentSettings, "pg_catalog.simple")
	check(3748)
	child := &session.RestrictedInvocation{Owner: owner, Parent: parent}
	op.Restricted = child
	childSettings := op.EffectiveSettings()
	if childSettings == parentSettings || childSettings == s.Settings {
		t.Fatal("child inherited mutable settings owner")
	}
	check(3748)
	assign(childSettings, "public.array_cfg_restricted")
	check(uint32(address.ObjectID))
	mark := op.CaptureInvocation()
	assign(childSettings, "pg_catalog.english")
	check(uint32(english.ObjectID))
	mark.Restore()
	check(uint32(address.ObjectID))
	// Detached checkpoint ownership survives further changes to the discarded
	// image and unwinds through the actual parent/physical invocation stack.
	assign(childSettings, "pg_catalog.simple")
	check(uint32(address.ObjectID))
	op.Restricted = parent
	check(3748)
	op.Restricted = nil
	check(uint32(english.ObjectID))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if v, err := runtime.CurrentTextSearchConfig(canceled, result); v.Type() != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("current-config cancellation published identity", v, err)
	}
	check(uint32(english.ObjectID))
}
