package functions

import (
	"strings"
	"testing"
)

func TestLookupClaimsRegisteredNames(t *testing.T) {
	for _, name := range []string{"uuid_extract_version", "uuid_extract_timestamp", "UUID_Extract_Version"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	for _, name := range []string{"", "uuid_extract", "uuid_extract_versions", "uuidv7"} {
		if definition, ok := Lookup(name); ok {
			t.Fatalf("Lookup(%q) claimed by %v", name, definition.Names)
		}
	}
}

func TestDefinitionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range definitions {
		if len(definition.Names) == 0 || definition.Bind == nil {
			t.Fatalf("incomplete definition %v", definition.Names)
		}
		for _, name := range definition.Names {
			key := strings.ToLower(name)
			if name == "" || seen[key] {
				t.Fatalf("empty or duplicate name %q", name)
			}
			seen[key] = true
		}
	}
}
