package settings

import "testing"

func TestPasswordCheckRegistrationDefinition(t *testing.T) {
	d := PasswordCheckDefinition()
	if d.Name != PasswordCheckMinimum || d.Description != "Minimum allowed password length." || d.Category != "Customized Options" || d.Type != Integer || d.Access != Privileged || d.Unit != Bytes || d.IntegerMin != 0 || d.IntegerMax != 2147483647 || d.ExtraDescription != "" || d.BootValue().Number != 8 { t.Fatal("source-owned metadata", d) }
	r := NewRegistry([]Definition{d}, false)
	d.BootDefault = "64"
	d.IntegerMax = 64
	actual, err := r.Lookup("PASSWORDCHECK.MIN_PASSWORD_LENGTH")
	if err != nil || actual.IntegerMax != 2147483647 || actual.Format(actual.DefaultValue()) != "8B" || actual.Raw(actual.BootValue()) != "8" { t.Fatal("mutable input leaked into frozen authority", actual, err) }
	defaults := r.SessionDefaults()
	if defaults.Typed[PasswordCheckMinimum].Number != 8 || defaults.Text[PasswordCheckMinimum] != "8B" { t.Fatal("trusted boot value not captured") }
	for _, input := range []string{"0", "1kB", "2147483647"} { if _, err := actual.Parse(input); err != nil { t.Fatal(input, err) } }
	for _, name := range []string{PasswordCheckMinimum, "PASSWORDCHECK.other"} { if _, err := Validate(Definition{Name: name, Type: String}); err == nil { t.Fatal("public spoof admitted", name) } }
	if _, exists := NewRegistry(nil, false).Definitions()[PasswordCheckMinimum]; exists { t.Fatal("default activation is not off") }
}
