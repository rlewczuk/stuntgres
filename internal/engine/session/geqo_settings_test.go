package session

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// Providers are an internal physical-owner seam, not a public PostgreSQL API.
func TestGeqoSettingsStartupDefaults(t *testing.T) {
	r := settings.NewRegistry(nil,false)
	provider := resourceDefaults{map[string]string{"geqo_pool_size":"1","geqo_seed":"0.125"},map[string]int{}}
	capture := func() *SettingsStore {
		s := &SettingsStore{Values:map[string]string{}}
		// Captured numeric defaults belong to this physical owner, not display text.
		if err := s.Initialize(t.Context(),r.Definitions(),r.Order(),provider); err != nil { t.Fatal(err) }
		return s
	}
	old := capture()
	if old.DefaultTyped["geqo_pool_size"].Number != 1 || old.DefaultTyped["geqo_seed"].Number != 0.125 { t.Fatal("numeric defaults not captured") }
	for name, want := range map[string]string{"geqo_pool_size":"1","geqo_seed":"0.125"} {
		if old.Value(name) != want || !old.ProviderDefaults[name] || old.DefaultSources[name] != DefaultSourceClient { t.Fatal(name,old.Value(name),old.DefaultSources[name]) }
		d,_ := r.Lookup(name)
		if d.Format(old.TypedValue(name)) != want || d.BootDefault != "0" { t.Fatal("typed capture or boot mutated",name) }
	}
	provider.values["geqo_pool_size"] = "2"
	provider.values["geqo_seed"] = "1"
	for _, commit := range []bool{false,true} {
		for _, name := range []string{"geqo_pool_size","geqo_seed"} {
			d,_ := r.Lookup(name)
			v,err := d.Parse(provider.values[name]); if err != nil { t.Fatal(err) }
			old.Touch(name,SettingLocal)
			old.Restore(name,settingOverride{Present:true,Text:d.Format(v),Typed:v})
		}
		old.FinishTransaction(commit)
		if old.Value("geqo_pool_size") != "1" || old.Value("geqo_seed") != "0.125" { t.Fatal("LOCAL lost captured defaults") }
	}
	// Individual RESET/DEFAULT removes an override rather than consulting providers.
	old.Restore("geqo_pool_size",settingOverride{Present:true,Text:"3",Typed:settings.Value{Number:3}})
	old.Restore("geqo_pool_size",settingOverride{})
	if old.Value("geqo_pool_size") != "1" || old.TypedValue("geqo_pool_size").Number != 1 { t.Fatal("override removal lost captured default") }
	old.Values["geqo_pool_size"] = "3"
	old.Values["geqo_seed"] = "0.5"
	old.ResetAllLocked(r)
	if old.Value("geqo_pool_size") != "1" || old.Value("geqo_seed") != "0.125" { t.Fatal("RESET recaptured provider") }
	for _, name := range []string{"geqo_pool_size","geqo_seed"} {
		if provider.calls[name] != 1 || !old.ProviderDefaults[name] || old.DefaultSources[name] != DefaultSourceClient { t.Fatal("reset changed provenance",name) }
	}
	fresh := capture()
	if fresh.Value("geqo_pool_size") != "2" || fresh.Value("geqo_seed") != "1" || old.Value("geqo_pool_size") != "1" { t.Fatal("physical capture not isolated") }
	if err := old.EndSession(ids.SessionToken{},0); err != nil { t.Fatal(err) }
	if old.DefaultTyped != nil || old.ProviderDefaults != nil || old.DefaultSources != nil { t.Fatal("retired physical owner retained defaults") }
	for _, tc := range []struct{name,input string}{{"geqo_pool_size","-1"},{"geqo_seed","1.01"}} {
		s := &SettingsStore{Values:map[string]string{}}
		if err := s.Initialize(t.Context(),r.Definitions(),r.Order(),physicalDefaultProvider{tc.name:tc.input}); err == nil { t.Fatal("provider bypassed validation",tc) }
	}
}
