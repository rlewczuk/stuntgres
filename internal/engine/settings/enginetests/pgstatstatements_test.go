package enginetests

import (
	"testing"

	"stuntgres/internal/engine/settings"
)

func TestPgStatStatementsSettings(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	for _, tc := range []struct {
		name, boot string
		kind       settings.Type
		access     settings.Access
	}{
		{"pg_stat_statements.track", "top", settings.Enum, settings.Privileged},
		{"pg_stat_statements.track_utility", "on", settings.Boolean, settings.Privileged},
		{"pg_stat_statements.track_planning", "off", settings.Boolean, settings.Privileged},
		{"pg_stat_statements.max", "5000", settings.Integer, settings.StartupOnly},
		{"pg_stat_statements.save", "on", settings.Boolean, settings.Sighup},
	} {
		definition, err := registry.Lookup(tc.name)
		if err != nil || definition.Type != tc.kind || definition.Access != tc.access || definition.BootDefault != tc.boot || definition.Format(definition.BootValue()) != tc.boot || definition.Format(definition.DefaultValue()) != tc.boot || definition.Category != "Customized Options" {
			t.Fatal(tc.name, definition, err)
		}
		flags, recognized := registry.LookupFlags(tc.name)
		if !recognized || definition.Flags != 0 || flags != 0 {
			t.Fatal("module flags differ from released zero-bit definitions", tc.name, definition.Flags, flags, recognized)
		}
	}
	track, _ := registry.Lookup("pg_stat_statements.track")
	for _, text := range []string{"none", "top", "all"} {
		value, err := track.Parse(text)
		if err != nil || track.Format(value) != text {
			t.Fatal(text, value, err)
		}
	}
	if _, err := track.Parse("invalid"); err == nil {
		t.Fatal("invalid tracking enum accepted")
	}
	limit, _ := registry.Lookup("pg_stat_statements.max")
	if limit.IntegerMin != 100 || limit.IntegerMax != 1073741823 {
		t.Fatal(limit)
	}
	for _, text := range []string{"100", "5000", "1073741823"} {
		if _, err := limit.Parse(text); err != nil {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"99", "1073741824"} {
		if _, err := limit.Parse(text); err == nil {
			t.Fatal("invalid capacity accepted", text)
		}
	}
}
