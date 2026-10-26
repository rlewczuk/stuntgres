package session

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestPresetSettingsProviderDefaults(t *testing.T) {
	custom,err := settings.Validate(settings.Definition{Name:"preset.custom",Type:settings.String,Access:settings.ReadOnly,BootDefault:"boot"}); if err != nil { t.Fatal(err) }
	r := settings.NewRegistry([]settings.Definition{custom},false)
	create := func(provider settings.SessionDefaultProvider) (*SettingsStore,error) {
		s := &SettingsStore{Defaults:make(map[string]string),Values:make(map[string]string)}
		err := s.Initialize(t.Context(),r.Definitions(),r.Order(),provider)
		return s,err
	}
	defaults := physicalDefaultProvider{
		"block_size":"8192", "data_checksums":"false", "data_directory_mode":"448", "debug_assertions":"off",
		"huge_pages_status":"OFF", "in_hot_standby":"off", "integer_datetimes":"true", "max_function_args":"100",
		"max_identifier_length":"63", "max_index_keys":"32", "num_os_semaphores":"0", "segment_size":"1GB",
		"server_encoding":"UTF8", "server_version":"18.0", "server_version_num":"180000", "shared_memory_size":"0MB",
		"shared_memory_size_in_huge_pages":"-1", "ssl_library":"", "wal_block_size":"8192", "wal_segment_size":"16MB",
		"preset.custom":"provider",
	}
	provider := &clientDefaultProvider{physicalDefaultProvider:defaults}
	s,err := create(provider); if err != nil { t.Fatal(err) }
	if provider.calls != len(r.Order()) || s.Value("preset.custom") != "provider" { t.Fatal(provider.calls,s.Value("preset.custom")) }
	for name := range defaults {
		if name == "preset.custom" { continue }
		d,_ := r.Lookup(name)
		if s.Value(name) != d.Format(d.DefaultValue()) || !s.ProviderDefaults[name] { t.Fatal(name,s.Value(name)) }
		alternate := "different"
		switch d.Type {
		case settings.Boolean: alternate = "on"; if d.Format(d.DefaultValue()) == "on" { alternate = "off" }
		case settings.Integer:
			if d.IntegerMin == d.IntegerMax { alternate = "1" } else { alternate = "1"; if name == "wal_segment_size" { alternate = "32MB" } }
		case settings.Enum: alternate = "on"
		}
		_,err := create(physicalDefaultProvider{name:alternate})
		if err == nil { t.Fatal("changed fixed preset",name) }
		// Fixed-domain inputs are rejected by typed parsing first; valid changed
		// values must reach the ordinary provider-boundary error.
		if _,parseErr := d.Parse(alternate); parseErr == nil && err.Error() != "stuntgres: default provider cannot change "+name { t.Fatal(name,err) }
	}
	s.ResetAllLocked(r)
	if provider.calls != len(r.Order()) { t.Fatal("RESET called provider",provider.calls) }
}

type presetFailingProvider struct { err error }
func (p presetFailingProvider) SessionDefault(context.Context,settings.Definition) (string,bool,error) { return "",false,p.err }

func TestPresetSettingsProviderFailure(t *testing.T) {
	r := settings.NewRegistry(nil,false)
	failure := errors.New("provider failure")
	s := &SettingsStore{Defaults:make(map[string]string)}
	if err := s.Initialize(t.Context(),r.Definitions(),r.Order(),presetFailingProvider{failure}); !errors.Is(err,failure) { t.Fatal(err) }
	ctx,cancel := context.WithCancel(t.Context()); cancel()
	if err := s.Initialize(ctx,r.Definitions(),r.Order(),nil); !errors.Is(err,context.Canceled) { t.Fatal(err) }
}

func TestPresetSettingsAuthorityCallback(t *testing.T) {
	r := settings.NewRegistry(nil,false)
	count := 0
	for _, name := range r.Order() {
		d := r.Definitions()[name]
		if !d.FixedDefault { continue }
		count++
		called := false
		err := SettingAccess(d,func(string) (bool,error) { called = true; return true,errors.New("must not consult authority") })
		var diagnostic *sqlstate.Error
		if called || !errors.As(err,&diagnostic) || diagnostic.Code != "55P02" || diagnostic.Message != "parameter \""+name+"\" cannot be changed" { t.Fatal(name,called,err) }
	}
	if count != 20 { t.Fatal("fixed preset count",count) }
}
