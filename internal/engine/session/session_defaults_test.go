package session

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
)

// A provider-less session's shared defaults equal those Initialize builds
// without a provider, and every such session of one registry reads the same
// maps.
func TestSharedSessionDefaultsMatchInitialize(t *testing.T) {
	for _, track := range []bool{false, true} {
		registry := settings.NewRegistry(nil, track)
		var built, shared, other SettingsStore
		if err := built.Initialize(context.Background(), registry.Definitions(), registry.Order(), nil); err != nil {
			t.Fatal(err)
		}
		shared.InitializeShared(registry.SessionDefaults())
		other.InitializeShared(settings.NewRegistry(nil, track).SessionDefaults())
		if !reflect.DeepEqual(built.Defaults, shared.Defaults) || !reflect.DeepEqual(built.DefaultTyped, shared.DefaultTyped) {
			t.Fatal("shared session defaults differ from initialized ones")
		}
		if reflect.ValueOf(shared.Defaults).Pointer() != reflect.ValueOf(other.Defaults).Pointer() || reflect.ValueOf(shared.DefaultTyped).Pointer() != reflect.ValueOf(other.DefaultTyped).Pointer() {
			t.Fatal("sessions of one registry built separate default maps")
		}
		if reflect.ValueOf(shared.Typed).Pointer() == reflect.ValueOf(other.Typed).Pointer() || shared.Placeholders == nil {
			t.Fatal("sessions share their own setting state")
		}
	}
}

func TestSharedSessionDefaultsPersistedOwnership(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	base := registry.SessionDefaults()
	baseText, baseTyped := maps.Clone(base.Text), maps.Clone(base.Typed)
	var target, sibling, private SettingsStore
	target.InitializeShared(base)
	sibling.InitializeShared(base)
	if err := private.Initialize(t.Context(), registry.Definitions(), registry.Order(), nil); err != nil {
		t.Fatal(err)
	}
	privateText, privateTyped := maps.Clone(private.Defaults), maps.Clone(private.DefaultTyped)
	before := cloneInvocationSettings(&target)
	captured := map[string]CapturedDefault{
		"search_path":             {Input: "login_schema, public", Source: DefaultSourceDatabaseUser},
		"application_name":        {Input: "login_app", Source: DefaultSourceUser},
		"role_settings.ownership": {Input: "custom_reset", Source: DefaultSourceDatabase},
	}
	if err := target.CapturePersistedDefaults(t.Context(), registry, captured); err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(target.Defaults).Pointer() == reflect.ValueOf(base.Text).Pointer() ||
		reflect.ValueOf(target.DefaultTyped).Pointer() == reflect.ValueOf(base.Typed).Pointer() {
		t.Fatal("persisted defaults retained registry backing")
	}
	wantText := maps.Clone(baseText)
	wantText["search_path"] = "login_schema, public"
	wantText["application_name"] = "login_app"
	wantText["role_settings.ownership"] = "custom_reset"
	wantTyped := maps.Clone(baseTyped)
	wantTyped["search_path"] = settings.Value{Text: "login_schema, public", Path: []string{"login_schema", "public"}}
	wantTyped["application_name"] = settings.Value{Text: "login_app"}
	wantTyped["role_settings.ownership"] = settings.Value{Text: "custom_reset"}
	wantSources := map[string]DefaultSource{
		"search_path": DefaultSourceDatabaseUser, "application_name": DefaultSourceUser,
		"role_settings.ownership": DefaultSourceDatabase,
	}
	if !reflect.DeepEqual(target.Defaults, wantText) || !reflect.DeepEqual(target.DefaultTyped, wantTyped) ||
		!reflect.DeepEqual(target.DefaultSources, wantSources) ||
		!reflect.DeepEqual(target.Placeholders, map[string]bool{"role_settings.ownership": true}) {
		t.Fatalf("incorrect populated reset image: text=%v typed=%v sources=%v placeholders=%v",
			target.Defaults, target.DefaultTyped, target.DefaultSources, target.Placeholders)
	}
	after := cloneInvocationSettings(&target)
	textBacking, typedBacking := reflect.ValueOf(target.Defaults).Pointer(), reflect.ValueOf(target.DefaultTyped).Pointer()
	if err := target.CapturePersistedDefaults(t.Context(), registry, map[string]CapturedDefault{
		"application_name": {Input: "replacement_app", Source: DefaultSourceGlobal},
	}); err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(target.Defaults).Pointer() != textBacking || reflect.ValueOf(target.DefaultTyped).Pointer() != typedBacking {
		t.Fatal("already owned reset maps were copied again")
	}
	if !reflect.DeepEqual(after.Defaults, wantText) || !reflect.DeepEqual(after.DefaultTyped, wantTyped) ||
		!reflect.DeepEqual(after.DefaultSources, wantSources) {
		t.Fatal("populated private invocation reset image changed with startup owner")
	}
	target.Values = map[string]string{"search_path": "live_schema"}
	target.Typed["search_path"] = settings.Value{Text: "live_schema", Path: []string{"live_schema"}}
	target.Restore("search_path", settingOverride{})
	if target.Value("search_path") != "login_schema, public" ||
		!reflect.DeepEqual(target.TypedValue("search_path"), wantTyped["search_path"]) {
		t.Fatal("reset did not restore the captured typed search path")
	}
	var future SettingsStore
	future.InitializeShared(registry.SessionDefaults())
	for name, store := range map[string]*SettingsStore{"sibling": &sibling, "future": &future, "private": &private, "before": before} {
		if !reflect.DeepEqual(store.Defaults, baseText) || !reflect.DeepEqual(store.DefaultTyped, baseTyped) ||
			len(store.DefaultSources) != 0 || len(store.Placeholders) != 0 {
			t.Fatalf("%s session reset image contaminated", name)
		}
	}
	if !reflect.DeepEqual(registry.SessionDefaults().Text, baseText) || !reflect.DeepEqual(registry.SessionDefaults().Typed, baseTyped) ||
		!reflect.DeepEqual(private.Defaults, privateText) || !reflect.DeepEqual(private.DefaultTyped, privateTyped) {
		t.Fatal("registry or privately initialized reset image mutated")
	}
	if reflect.ValueOf(sibling.Defaults).Pointer() != reflect.ValueOf(base.Text).Pointer() ||
		reflect.ValueOf(future.DefaultTyped).Pointer() != reflect.ValueOf(base.Typed).Pointer() {
		t.Fatal("unmodified sessions stopped sharing registry reset maps")
	}
}

func TestSharedSessionDefaultsLazyOwnership(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	base := registry.SessionDefaults()
	for _, captured := range []map[string]CapturedDefault{
		nil,
		{"role": {Input: "startup_role", Source: DefaultSourceUser}},
	} {
		var store SettingsStore
		store.InitializeShared(base)
		if err := store.CapturePersistedDefaults(t.Context(), registry, captured); err != nil {
			t.Fatal(err)
		}
		if reflect.ValueOf(store.Defaults).Pointer() != reflect.ValueOf(base.Text).Pointer() ||
			reflect.ValueOf(store.DefaultTyped).Pointer() != reflect.ValueOf(base.Typed).Pointer() {
			t.Fatal("startup without ordinary persisted overrides copied reset maps")
		}
	}
	var provider SettingsStore
	if err := provider.Initialize(t.Context(), registry.Definitions(), registry.Order(),
		physicalDefaultProvider{"search_path": base.Text["search_path"]}); err != nil {
		t.Fatal(err)
	}
	textBacking, typedBacking := reflect.ValueOf(provider.Defaults).Pointer(), reflect.ValueOf(provider.DefaultTyped).Pointer()
	if err := provider.CapturePersistedDefaults(t.Context(), registry, map[string]CapturedDefault{
		"search_path":      {Input: "ignored_schema", Source: DefaultSourceUser},
		"application_name": {Input: "persisted_app", Source: DefaultSourceDatabase},
	}); err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(provider.Defaults).Pointer() != textBacking || reflect.ValueOf(provider.DefaultTyped).Pointer() != typedBacking {
		t.Fatal("provider-owned reset maps were copied")
	}
	if provider.Defaults["search_path"] != base.Text["search_path"] ||
		!reflect.DeepEqual(provider.DefaultTyped["search_path"], base.Typed["search_path"]) ||
		provider.DefaultSources["search_path"] != DefaultSourceClient || !provider.ProviderDefaults["search_path"] ||
		provider.Defaults["application_name"] != "persisted_app" ||
		!reflect.DeepEqual(provider.DefaultTyped["application_name"], settings.Value{Text: "persisted_app"}) ||
		provider.DefaultSources["application_name"] != DefaultSourceDatabase {
		t.Fatal("provider precedence or persisted reset image lost")
	}
}
