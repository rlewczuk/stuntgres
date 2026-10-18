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
	// The installed uuid-ossp routine sat before the shared gates and matched
	// every spelling; its installed lookup owns exact resolution.
	for _, name := range []string{"uuid_generate_v4", "UUID_Generate_V4"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || definition.ExactSpelling || definition.SharedGates {
			t.Fatalf("Lookup(%q) = %+v, %v", name, definition.Names, ok)
		}
	}
	// The array and range families sat before the shared gates and matched names
	// case-insensitively; the bind code owns exact-spelling resolution.
	for _, name := range []string{"array_dims", "Cardinality", "array_sample", "ARRAY_SHUFFLE", "array_append", "ARRAY_SORT", "trim_array", "array_to_string", "array_position", "array_positions", "Array_Remove", "array_replace", "IsEmpty", "lower_inc", "UPPER_INF", "range_merge", "multirange"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || definition.ExactSpelling || definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	// JSON calls keep each chain predicate's comparison: folded names,
	// lowercased JSON creation and record names, and the exact text-object
	// names of the final switch.
	for _, name := range []string{"to_json", "Row_To_JSON", "JSON_TYPEOF", "jsonb_set", "JSONB_PATH_QUERY_ARRAY", "json_extract_path", "JSON_EXTRACT_PATH", "jsonb_populate_record_valid", "j\u017fon_typeof"} {
		definition, ok := Lookup(name)
		if !ok || definition.Claims == nil || definition.ExactSpelling || definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	for _, name := range []string{"json_object", "jsonb_object"} {
		definition, ok := Lookup(name)
		if !ok || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	for _, name := range []string{"extract", "date_part", "date_trunc", "date_bin", "timezone", "date_add", "justify_interval", "to_char", "to_timestamp", "make_date", "make_interval"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
		// Only the make_* constructors accept argument labels.
		if definition.OwnsLabels != strings.HasPrefix(name, "make_") {
			t.Fatalf("Lookup(%q).OwnsLabels = %v", name, definition.OwnsLabels)
		}
	}
	for _, name := range []string{"left", "trim", "split_part", "string_to_array", "position", "md5", "unicode_assigned"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates || definition.OwnsLabels {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	// parse_ident and lower/upper sat before the shared gates and claimed
	// any spelling.
	for _, name := range []string{"parse_ident", "PARSE_IDENT", "Parse_Ident", "lower", "UPPER", "Lower"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || definition.ExactSpelling || definition.SharedGates {
			t.Fatalf("Lookup(%q) = %v, %v", name, definition.Names, ok)
		}
	}
	if definition, ok := Lookup("like_escape"); !ok || !definition.ExactSpelling || !definition.SharedGates || definition.OwnsLabels {
		t.Fatalf("Lookup(like_escape) = %v, %v", definition.Names, ok)
	}
	// regexp_* checks its own labels before the shared gates, and
	// similar_to_escape is dispatched by call syntax, not by name.
	for _, name := range []string{"", "uuid_generate_v1", "uuid_generate_v4x", "formats", "format_type", "uuid_extract", "uuid_extract_versions", "UUIDv7", "uuidv1", "uuidv", "ABS", "Width_Bucket", "random", "absolute", "unnest", "generate_subscripts", "array_agg", "array_cat_agg", "array", "range_agg", "int4range",
		"JSON_OBJECT", "json_agg", "jsonb_object_agg", "json", "to_j\u017fon", "jsonb_populate_record\u017fet", "json_array", "jsonb_path_query_arrays",
		"Date_Trunc", "MAKE_DATE", "age", "isfinite", "overlaps", "pg_sleep", "now", "to_number",
		"LIKE_ESCAPE", "regexp_match", "regexp_replace", "similar_to_escape", "similar_escape",
		// string_to_table and the variadic family keep engine-owned analyzer
		// state; substring routes through the binary family.
		"LEFT", "Trim", "string_to_table", "concat", "concat_ws", "quote_literal", "substring", "substr"} {
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
		if definition.Claims != nil && definition.ExactSpelling {
			t.Fatalf("definition %v sets both Claims and ExactSpelling", definition.Names)
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
			if definition.Claims != nil && (name != key || !definition.Claims(name)) {
				t.Fatalf("name %q is not lowercase or not claimed by its predicate", name)
			}
			seen[key] = true
		}
	}
}
