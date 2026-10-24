package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestPGSettingsProviderCapture(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "", "statement_timeout": "2s"}, calls: map[string]int{}}
	a := sessionStart(t, e, provider)
	capture := func(s *session.Session) map[string]string {
		t.Helper()
		facts, err := (engineCatalogRuntime{settings: s.Settings, registry: e.settings}).SessionSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]string)
		for _, f := range facts {
			out[f.Definition.Name] = f.Definition.Raw(f.Value) + "/" + f.Source + "/" + f.Definition.Raw(f.Reset)
		}
		return out
	}
	initial := capture(a)
	if initial["application_name"] != "/client/" || initial["statement_timeout"] != "2000/client/2000" || initial["row_security"] != "on/default/on" {
		t.Fatal(initial)
	}
	provider.values["application_name"] = "replacement"
	provider.values["statement_timeout"] = "3s"
	apply := func(value *string) {
		t.Helper()
		op, err := a.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		if _, err := e.applySetting(op, a.Settings, e.settings.Definitions()["application_name"], value, false); err != nil {
			t.Fatal(err)
		}
	}
	empty := ""
	apply(&empty)
	a.Settings.Mu.Lock()
	a.Settings.CurrentIsolation = "repeatable read"
	a.Settings.CurrentReadOnly = true
	a.Settings.CurrentDeferrable = true
	a.Settings.Mu.Unlock()
	current := capture(a)
	if current["application_name"] != "/session/" || current["transaction_isolation"] != "repeatable read/default/read committed" || current["transaction_read_only"] != "on/default/off" || current["transaction_deferrable"] != "on/default/off" {
		t.Fatal(current)
	}
	apply(nil)
	a.Settings.FinishTransaction(true)
	if got := capture(a); got["application_name"] != initial["application_name"] || got["statement_timeout"] != initial["statement_timeout"] {
		t.Fatal(got)
	}
	b := sessionStart(t, e, provider)
	if got := capture(b); got["application_name"] != "replacement/client/replacement" || got["statement_timeout"] != "3000/client/3000" {
		t.Fatal(got)
	}
	if capture(a)["application_name"] != initial["application_name"] {
		t.Fatal("peer changed captured defaults")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if facts, err := (engineCatalogRuntime{settings: a.Settings, registry: e.settings}).SessionSettings(ctx); facts != nil || err != context.Canceled {
		t.Fatal(facts, err)
	}
	store := &session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := store.Initialize(t.Context(), e.settings.Definitions(), e.settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ids.SessionToken{}, session.CleanClose); err != nil || store.ProviderDefaults != nil || store.DefaultTyped != nil {
		t.Fatal(store, err)
	}
}

func TestPGSettingsCoherentCapture(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			s.Settings.Mu.Lock()
			value := "even"
			if i%2 != 0 {
				value = "odd"
			}
			s.Settings.Typed["application_name"] = settings.Value{Text: value}
			s.Settings.Typed["lc_messages"] = settings.Value{Text: value}
			s.Settings.Mu.Unlock()
		}
	}()
	defer func() { <-done }()
	for range 1000 {
		facts, err := (engineCatalogRuntime{settings: s.Settings, registry: e.settings}).SessionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var application, locale string
		for _, f := range facts {
			if f.Definition.Name == "application_name" {
				application = f.Value.Text
			}
			if f.Definition.Name == "lc_messages" {
				locale = f.Value.Text
			}
		}
		if application != "" && application != locale {
			t.Errorf("torn capture %s/%s", application, locale)
			break
		}
	}
	<-done
}
