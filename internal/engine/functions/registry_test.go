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
	// Families that sat in the engine's final name switch keep its exact
	// spelling and its shared label/ResolutionOnly gates.
	for _, name := range []string{"abs", "width_bucket", "log10", "atan2d", "trim_scale"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	// UUID generation sat in the final switch; uuidv7 kept its shift label
	// past the shared label gate.
	for _, name := range []string{"uuidv4", "uuidv7"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates || definition.OwnsLabels != (name == "uuidv7") {
			t.Fatalf("Lookup(%q) = %+v, %v", name, definition.Names, ok)
		}
	}
	// format claims every spelling; shared gates send other spellings to the
	// resolution-only diagnostics, as the engine's chain did.
	for _, name := range []string{"format", "FORMAT", "Format"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || definition.ExactSpelling || !definition.SharedGates || definition.OwnsLabels {
			t.Fatalf("Lookup(%q) = %+v, %v", name, definition.Names, ok)
		}
	}
	for _, name := range []string{"", "formats", "format_type", "uuid_extract", "uuid_extract_versions", "UUIDv7", "uuidv1", "uuidv", "ABS", "Width_Bucket", "random", "absolute"} {
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
		if definition.OwnsLabels && !definition.SharedGates {
			t.Fatalf("definition %v owns labels without shared gates", definition.Names)
		}
		for _, name := range definition.Names {
			key := strings.ToLower(name)
			if name == "" || seen[key] {
				t.Fatalf("empty or duplicate name %q", name)
			}
			// The parser folds unquoted names, so an exact name is lowercase.
			if definition.ExactSpelling && name != key {
				t.Fatalf("exact name %q is not lowercase", name)
			}
			seen[key] = true
		}
	}
}
