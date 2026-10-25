package session

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/settings"
)

type physicalDefaultProvider map[string]string

func (p physicalDefaultProvider) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	v, present := p[d.Name]
	return v, present, nil
}

func TestPhysicalSettingsDefaultCapture(t *testing.T) {
	custom, err := settings.Validate(settings.Definition{Name: "physical.empty", Type: settings.String})
	if err != nil {
		t.Fatal(err)
	}
	registry := settings.NewRegistry([]settings.Definition{custom}, false)
	defaults := physicalDefaultProvider{
		"config_file":       "/stuntgres/simulated/postgresql.conf",
		"data_directory":    "/stuntgres/simulated/data",
		"external_pid_file": "",
		"hba_file":          "/stuntgres/simulated/pg_hba.conf",
		"ident_file":        "/stuntgres/simulated/pg_ident.conf",
	}
	newStore := func(provider settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
		err := s.Initialize(t.Context(), registry.Definitions(), registry.Order(), provider)
		return s, err
	}
	for _, provider := range []settings.SessionDefaultProvider{nil, physicalDefaultProvider{}, defaults} {
		s, err := newStore(provider)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range defaults {
			d := registry.Definitions()[name]
			if s.Value(name) != value || s.Defaults[name] != value || !reflect.DeepEqual(s.DefaultTyped[name], d.DefaultValue()) {
				t.Fatal(name, s.Value(name), s.DefaultTyped[name])
			}
			suppliedValues, _ := provider.(physicalDefaultProvider)
			_, supplied := suppliedValues[name]
			if s.ProviderDefaults[name] != supplied {
				t.Fatal("provider provenance lost", name, s.ProviderDefaults)
			}
		}
		if value, present := s.Defaults["physical.empty"]; !present || value != "" || registry.Definitions()["physical.empty"].BootDefaultNull {
			t.Fatal("custom empty default lost", value, present)
		}
	}
	for name := range defaults {
		_, err := newStore(physicalDefaultProvider{name: "/different/startup/location"})
		if err == nil || err.Error() != "stuntgres: default provider cannot change "+name {
			t.Fatal("provider changed startup assignment", name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := s.Initialize(ctx, registry.Definitions(), registry.Order(), nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", err)
	}
}

type clientDefaultProvider struct {
	physicalDefaultProvider
	calls int
}

func (p *clientDefaultProvider) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	p.calls++
	return p.physicalDefaultProvider.SessionDefault(ctx, d)
}

func TestClientSettingsProviderCapture(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	provider := &clientDefaultProvider{physicalDefaultProvider: physicalDefaultProvider{
		"default_table_access_method": strings.Repeat("x", 64),
		"default_tablespace":          "provider_missing_space",
		"temp_tablespaces":            "provider_missing_space",
		"default_text_search_config":  "provider_missing_config",
		"gin_pending_list_limit":      "8MB",
		"createrole_self_grant":       "SET, INHERIT",
	}}
	newStore := func() *SettingsStore {
		s := &SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
		if err := s.Initialize(t.Context(), registry.Definitions(), registry.Order(), provider); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := newStore()
	if provider.calls != len(registry.Order()) {
		t.Fatal("provider was not called once per definition", provider.calls)
	}
	want := map[string]string{
		"default_table_access_method": strings.Repeat("x", 63),
		"default_tablespace":          "provider_missing_space",
		"temp_tablespaces":            "provider_missing_space",
		"default_text_search_config":  "provider_missing_config",
		"gin_pending_list_limit":      "8MB",
		"createrole_self_grant":       "SET, INHERIT",
	}
	for name, value := range want {
		if s.Value(name) != value || !s.ProviderDefaults[name] {
			t.Fatal("trusted provider capture", name, s.Value(name), s.ProviderDefaults)
		}
		s.Values[name] = "changed"
		s.Typed[name] = settings.Value{Text: "changed"}
	}
	if s.DefaultTyped["gin_pending_list_limit"].Number != 8192 {
		t.Fatal("provider lost typed units", s.DefaultTyped["gin_pending_list_limit"])
	}
	provider.physicalDefaultProvider["default_tablespace"] = "later_provider_space"
	s.ResetAllLocked(registry)
	if provider.calls != len(registry.Order()) {
		t.Fatal("reset re-entered provider", provider.calls)
	}
	for name, value := range want {
		if s.Value(name) != value {
			t.Fatal("reset re-entered provider or catalog", name, s.Value(name), value)
		}
	}
	replacement := newStore()
	if provider.calls != 2*len(registry.Order()) {
		t.Fatal("replacement failed to recapture provider defaults", provider.calls)
	}
	if replacement.Value("default_tablespace") != "later_provider_space" || s.Value("default_tablespace") != "provider_missing_space" {
		t.Fatal("physical defaults were not independently captured")
	}
}
