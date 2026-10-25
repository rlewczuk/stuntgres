package session

import (
	"context"
	"errors"
	"reflect"
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
		"config_file": "/stuntgres/simulated/postgresql.conf",
		"data_directory": "/stuntgres/simulated/data",
		"external_pid_file": "",
		"hba_file": "/stuntgres/simulated/pg_hba.conf",
		"ident_file": "/stuntgres/simulated/pg_ident.conf",
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
