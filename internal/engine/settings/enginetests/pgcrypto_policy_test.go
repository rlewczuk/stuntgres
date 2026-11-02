package enginetests_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestPgcryptoPolicyDefinition(t *testing.T) {
	d := settings.PgcryptoDefinition()
	if d.Name != "pgcrypto.builtin_crypto_enabled" || d.Type != settings.Enum || d.Access != settings.Privileged || d.BootDefault != "on" || d.DefaultValue().Text != "on" || d.BootValue().Text != "on" || d.Category != "Customized Options" || d.Description != "Sets if builtin crypto functions are enabled." || d.ExtraDescription != `"on" enables builtin crypto, "off" unconditionally disables and "fips" will disable builtin crypto if OpenSSL is in FIPS mode` || d.Flags != 0 || d.ReadRestricted || d.Normalize != nil || d.NativeStartup != nil || !reflect.DeepEqual(d.EnumValues, []string{"on", "off", "fips"}) {
		t.Fatal("module recipe metadata", d)
	}
	for _, tc := range []struct{ input, want string }{{"ON", "on"}, {"oFf", "off"}, {"FIPS", "fips"}} {
		value, err := d.Parse(tc.input)
		if err != nil || d.Format(value) != tc.want { t.Fatal(tc, value, err) }
	}
	for _, input := range []string{"bogus", "true", "false", "1", "o", "fi", " fips ", ""} {
		_, err := d.Parse(input)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `invalid value for parameter "pgcrypto.builtin_crypto_enabled": "`+input+`"` || diagnostic.Hint != "Available values: on, off, fips." { t.Fatal(input, err) }
	}
	registry := settings.NewRegistry(nil, false)
	if _, err := registry.Lookup(d.Name); err == nil { t.Fatal("module recipe leaked into frozen global registry") }
	other := settings.PgcryptoDefinition()
	d.EnumValues[0] = "changed private copy"
	if other.EnumValues[0] != "on" || settings.PgcryptoDefinition().EnumValues[0] != "on" || d.BootValue().Text != "on" { t.Fatal("module recipe shares mutable enum/default backing") }
	if _, err := settings.Validate(settings.Definition{Name: "PGCRYPTO.BUILTIN_CRYPTO_ENABLED", Type: settings.String}); err == nil { t.Fatal("public registration can replace the private module recipe") }
}
