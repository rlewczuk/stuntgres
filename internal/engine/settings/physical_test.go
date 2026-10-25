package settings

import (
	"reflect"
	"testing"
)

func TestPhysicalSettingsDefinitions(t *testing.T) {
	registry := NewRegistry(nil, false)
	for _, tc := range []struct{ name, value, description string }{
		{"config_file", "/stuntgres/simulated/postgresql.conf", "Sets the server's main configuration file."},
		{"data_directory", "/stuntgres/simulated/data", "Sets the server's data directory."},
		{"external_pid_file", "", "Writes the postmaster PID to the specified file."},
		{"hba_file", "/stuntgres/simulated/pg_hba.conf", `Sets the server's "hba" configuration file.`},
		{"ident_file", "/stuntgres/simulated/pg_ident.conf", `Sets the server's "ident" configuration file.`},
	} {
		d, err := registry.Lookup(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if d.Type != String || d.Access != StartupOnly || !d.BootDefaultNull || !d.ReadRestricted || d.Category != "File Locations" || d.Description != tc.description || d.ExtraDescription != "" || d.Unit != UnitNone || len(d.EnumValues) != 0 {
			t.Fatalf("%s descriptor: %+v", tc.name, d)
		}
		if d.NativeStartup == nil || d.NativeStartup.Value != tc.value || d.NativeStartup.ResetNull != (tc.name == "external_pid_file") {
			t.Fatalf("%s startup: %+v", tc.name, d.NativeStartup)
		}
		if d.BootDefault != "" || d.BootValue().Text != "" || d.DefaultValue().Text != tc.value || d.Format(d.DefaultValue()) != tc.value {
			t.Fatalf("%s boot/current distinction: %+v", tc.name, d)
		}
	}
	custom, err := Validate(Definition{Name: "physical.empty", Type: String})
	if err != nil || custom.BootDefaultNull || custom.ReadRestricted || custom.NativeStartup != nil || custom.DefaultValue().Text != "" {
		t.Fatal("ordinary empty-string default must remain non-NULL", custom, err)
	}
	for _, d := range []Definition{
		{Name: "physical.invalid", Type: String, BootDefaultNull: true},
		{Name: "physical.invalid", Type: String, ReadRestricted: true},
		{Name: "physical.invalid", Type: String, NativeStartup: &StartupDefault{Value: "/forbidden"}},
	} {
		if _, err := Validate(d); err == nil {
			t.Fatal("custom definition accepted builtin-only metadata", d)
		}
	}
	for _, name := range registry.Order() {
		d := registry.Definitions()[name]
		if d.NativeStartup == nil && !reflect.DeepEqual(d.DefaultValue(), d.BootValue()) {
			t.Fatal("ordinary builtin changed effective default", name)
		}
	}
}
