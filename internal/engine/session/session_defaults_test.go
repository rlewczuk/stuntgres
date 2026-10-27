package session

import (
	"context"
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
