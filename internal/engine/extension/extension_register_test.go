package extension

import "testing"

// Register allocates a nil registry and adds only valid, new names; a
// rejected registration leaves the registry as it was.
func TestRegisterAddsOnlyValidImplementations(t *testing.T) {
	registry, err := Register(nil, "unaccent", StockUnaccent)
	if err != nil || len(registry) != 1 || registry["unaccent"] != StockUnaccent {
		t.Fatal("first registration", registry, err)
	}
	for _, tc := range []struct {
		why, name      string
		implementation Installer
	}{
		{"duplicate registration accepted", "unaccent", StockUnaccent},
		{"nil implementation accepted", "other", nil},
		{"implementation without a version accepted", "other", &UnaccentProvider{}},
		{"empty name accepted", "", StockFuzzystrmatch},
		{"invalid name accepted", "bad--name", StockFuzzystrmatch},
	} {
		next, err := Register(registry, tc.name, tc.implementation)
		if err == nil || next != nil {
			t.Fatal(tc.why, next)
		}
		if len(registry) != 1 || registry["unaccent"] != StockUnaccent {
			t.Fatal("rejected registration changed the registry", tc.why, registry)
		}
	}
}
