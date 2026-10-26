package session

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestDeveloperSettingsDefaultCapture(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	capture := func(ctx context.Context, p settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
		err := s.Initialize(ctx, registry.Definitions(), registry.Order(), p)
		return s, err
	}
	provider := &clientDefaultProvider{physicalDefaultProvider: physicalDefaultProvider{"ignore_system_indexes": "on", "jit_debugging_support": "true", "jit_profiling_support": "1", "post_auth_delay": "60s", "trace_sort": "on", "backtrace_functions": "foo,, 9bad,"}}
	old, err := capture(t.Context(), provider)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ignore_system_indexes": "on", "jit_debugging_support": "on", "jit_profiling_support": "on", "post_auth_delay": "1min", "trace_sort": "on", "backtrace_functions": "foo,, 9bad,"}
	if old.DefaultTyped["post_auth_delay"].Number != 60 {
		t.Fatal("lost typed delay")
	}
	for name, value := range want {
		if old.Value(name) != value || !old.ProviderDefaults[name] {
			t.Fatal("capture", name, old.Value(name))
		}
		d := registry.Definitions()[name]
		boot := "off"
		if name == "post_auth_delay" {
			boot = "0"
		}
		if name == "backtrace_functions" {
			boot = ""
		}
		if d.BootDefault != boot {
			t.Fatal("boot changed", name, d.BootDefault)
		}
		provider.physicalDefaultProvider[name] = d.BootDefault
	}
	old.Values["trace_sort"] = "off"
	old.Typed["trace_sort"] = settings.Value{}
	old.Values["backtrace_functions"] = "other"
	old.Typed["backtrace_functions"] = settings.Value{Text: "other"}
	old.ResetAllLocked(registry)
	if provider.calls != len(registry.Order()) {
		t.Fatal("provider reentered", provider.calls)
	}
	for name, value := range want {
		if old.Value(name) != value {
			t.Fatal("reset lost capture", name)
		}
	}
	replacement, err := capture(t.Context(), provider)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range want {
		d := registry.Definitions()[name]
		if replacement.Value(name) != d.Format(d.DefaultValue()) || old.Value(name) != value {
			t.Fatal("replacement isolation", name)
		}
	}
	for _, tc := range []struct{ name, alias, changed string }{{"ignore_invalid_pages", "false", "on"}, {"pre_auth_delay", "0s", "1"}, {"remove_temp_files_after_crash", "true", "off"}, {"send_abort_for_crash", "0", "on"}, {"send_abort_for_kill", "false", "on"}, {"debug_io_direct", "", "data"}} {
		if _, err := capture(t.Context(), physicalDefaultProvider{tc.name: tc.alias}); err != nil {
			t.Fatal("same initial alias", tc, err)
		}
		if _, err := capture(t.Context(), physicalDefaultProvider{tc.name: tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name {
			t.Fatal("changed process default", tc, err)
		}
	}
	for name, input := range map[string]string{"ignore_system_indexes": "broken", "jit_debugging_support": "broken", "jit_profiling_support": "broken", "post_auth_delay": "2148", "backtrace_functions": "foo-bar"} {
		if _, err := capture(t.Context(), physicalDefaultProvider{name: input}); err == nil {
			t.Fatal("invalid capture admitted", name)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := capture(ctx, provider); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", err)
	}
}

func TestDeveloperSettingsAccessAndReset(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	store := &SettingsStore{Values: map[string]string{}, Typed: map[string]settings.Value{}}
	for _, name := range registry.Order() {
		d := registry.Definitions()[name]
		if d.Category != "Developer Options" {
			continue
		}
		store.Values[name] = "sentinel"
		called := false
		err := SettingAccess(d, func(string) (bool, error) { called = true; return true, nil })
		if d.Access == settings.Session || d.Access == settings.Privileged {
			if err != nil || called != (d.Access == settings.Privileged) {
				t.Fatal("mutable authority", name, err, called)
			}
		} else {
			var e *sqlstate.Error
			if called || !errors.As(err, &e) || e.Code != "55P02" {
				t.Fatal("immutable authority", name, err, called)
			}
		}
	}
	store.ResetAllLocked(registry)
	for _, name := range registry.Order() {
		d := registry.Definitions()[name]
		if d.Category != "Developer Options" {
			continue
		}
		_, present := store.Values[name]
		if present != (d.Access != settings.Session && d.Access != settings.Privileged) {
			t.Fatal("reset class eligibility", name, present)
		}
	}
}
