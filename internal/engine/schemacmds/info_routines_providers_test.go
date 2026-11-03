package schemacmds_test

import (
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestInfoRoutinesBuiltinCompleteness(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	seen := make(map[ids.OID]bool)
	for _, declaration := range routine.BuiltinRoutineDeclarations {
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID})
		if seen[p.Address().ObjectID] || p.Builtin == nil || !p.Display.Valid() || p.SecurityDefiner || p.Display.Source == "see system_functions.sql" {
			t.Fatal("incomplete real builtin declaration", p)
		}
		seen[p.Address().ObjectID] = true
	}
	for _, oid := range []ids.OID{3329, 3400, 4100, 5052, 6250, 6399} {
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if seen[oid] || p.Builtin == nil || p.Builtin != routine.BuiltinRoutine(oid) || p.Builtin.Providers != 0 || !p.Builtin.ReturnsSet || !p.Display.Valid() || p.SecurityDefiner {
			t.Fatal("incomplete metadata-only backing declaration", p)
		}
		seen[oid] = true
	}
	memory := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2282})
	if !seen[2282] || memory.Builtin != routine.BuiltinRoutine(2282) || memory.Builtin.Providers == 0 || !memory.Builtin.ReturnsSet || memory.Result != 2249 || len(memory.ACL.Entries) != 2 {
		t.Fatal("backend memory must own one callable restricted declaration", memory)
	}
	for _, d := range view.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok {
			if !p.Display.Valid() || p.SecurityDefiner {
				t.Fatal("uninitialized real provider", p)
			}
			if p.Builtin != nil && !seen[p.Address().ObjectID] {
				t.Fatal("unowned builtin row", p)
			}
			if p.PLHandler != catalog.NoPLHandler {
				name, _, _ := p.PLHandler.Declaration()
				if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/plpgsql", HasBinary: true}) || p.Strict != (p.PLHandler != catalog.PLCallHandler) || p.Language != (ids.ObjectAddress{}) || p.Installed.Parallel != 'u' || p.Parallel != 0 {
					t.Fatal("PL canonical declaration", p)
				}
			}
			if p.Conversion.Name() != "" && (p.Display.Language != catalog.CRoutineLanguage || p.Display.Source != p.Conversion.Name() || !p.Display.HasBinary || p.Display.Binary == "") {
				t.Fatal("conversion declaration", p)
			}
		}
	}
	for oid, want := range map[ids.OID]catalog.RoutineDisplay{
		2282: {Language: catalog.InternalRoutineLanguage, Source: "pg_get_backend_memory_contexts"},
		3329: {Language: catalog.InternalRoutineLanguage, Source: "show_all_file_settings"},
		6250: {Language: catalog.InternalRoutineLanguage, Source: "pg_ident_file_mappings"},
		1397: {Language: catalog.InternalRoutineLanguage, Source: "int4abs"},
		3100: {Language: catalog.InternalRoutineLanguage, Source: "window_row_number", Support: 6233},
		2147: {Language: catalog.InternalRoutineLanguage, Source: "aggregate_dummy", Support: 6236},
		879:  {Language: catalog.SQLRoutineLanguage},
		1426: {Language: catalog.SQLRoutineLanguage},
		1544: {Language: catalog.SQLRoutineLanguage},
		1811: {Language: catalog.SQLRoutineLanguage},
		1708: {Language: catalog.SQLRoutineLanguage},
		1285: {Language: catalog.SQLRoutineLanguage, Source: "select pg_catalog.quote_literal($1::pg_catalog.text)"},
		3:    {Language: catalog.InternalRoutineLanguage, Source: "heap_tableam_handler"},
		330:  {Language: catalog.InternalRoutineLanguage, Source: "bthandler"},
	} {
		if got := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}).Display; got != want {
			t.Fatal("released builtin fact", oid, got, want)
		}
		if oid == 879 || oid == 1426 || oid == 1544 || oid == 1811 || oid == 1708 {
			if got := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}).Cost; got != 1 {
				t.Fatal("released builtin cost", oid, got)
			}
		}
	}
}

func TestInfoRoutinesExtensionCompleteness(t *testing.T) {
	// Independent release-source bags for every currently materialized member,
	// including duplicate source symbols and exceptional non-C declarations.
	for _, tc := range []struct {
		name     string
		language catalog.RoutineLanguage
		sources  []string
	}{
		{"unaccent", catalog.CRoutineLanguage, []string{"unaccent_dict", "unaccent_dict", "unaccent_init", "unaccent_lexize"}},
		{"pgcrypto", catalog.CRoutineLanguage, []string{"pg_digest", "pg_digest", "pg_hmac", "pg_hmac", "pg_crypt", "pg_gen_salt", "pg_gen_salt_rounds", "pg_encrypt", "pg_decrypt", "pg_encrypt_iv", "pg_decrypt_iv", "pg_random_bytes", "pg_random_uuid", "pg_check_fipsmode", "pg_armor", "pg_armor", "pg_dearmor", "pgp_armor_headers", "pgp_sym_encrypt_text", "pgp_sym_encrypt_text", "pgp_sym_decrypt_text", "pgp_sym_decrypt_text"}},
		{"uuid-ossp", catalog.CRoutineLanguage, []string{"uuid_nil", "uuid_ns_dns", "uuid_ns_url", "uuid_ns_oid", "uuid_ns_x500", "uuid_generate_v1", "uuid_generate_v1mc", "uuid_generate_v3", "uuid_generate_v4", "uuid_generate_v5"}},
		{"fuzzystrmatch", catalog.CRoutineLanguage, []string{"daitch_mokotoff", "levenshtein", "levenshtein_with_costs", "levenshtein_less_equal", "levenshtein_less_equal_with_costs", "metaphone", "dmetaphone", "dmetaphone_alt", "soundex", "difference"}},
		{"citext", catalog.InternalRoutineLanguage, []string{
			"textin", "textout", "citext_eq", "citext_ne", "citext_lt", "citext_le", "citext_gt", "citext_ge", "rtrim1", "booltext", "network_show", "texticlike", "texticlike", "texticnlike", "texticnlike", "texticregexeq", "texticregexeq", "texticregexne", "texticregexne",
			"", "", // regexp_match
			"", "", // regexp_matches
			"", "", // regexp_replace
			"", "", // regexp_split_to_array
			"", "", // regexp_split_to_table
			"", "", "", "", // strpos, replace, split_part, translate: unnamed SQL scalars
		}},
		{"intarray", catalog.CRoutineLanguage, []string{"bqarr_in", "bqarr_out", "_intbig_in", "_intbig_out"}},
		{"isn", catalog.CRoutineLanguage, []string{"ean13_in", "ean13_out", "isbn_in", "isn_out", "isbn_in", "ean13_out", "ismn_in", "isn_out", "ismn_in", "ean13_out", "issn_in", "isn_out", "issn_in", "ean13_out", "upc_in", "isn_out"}},
		{"intagg", catalog.InternalRoutineLanguage, []string{"aggregate_dummy", "array_unnest"}},
		{"pgrowlocks", catalog.CRoutineLanguage, []string{"pgrowlocks"}},
		{"cube", catalog.CRoutineLanguage, []string{"cube_in", "cube_out", "cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge", "cube_cmp", "cube_f8", "cube_f8_f8", "cube_a_f8", "cube_a_f8_f8", "cube_c_f8", "cube_c_f8_f8", "cube_dim", "cube_is_point", "cube_ll_coord", "cube_ur_coord", "cube_coord", "cube_coord_llur", "cube_subset", "cube_union", "cube_inter", "cube_enlarge", "cube_overlap", "cube_contains", "cube_contained", "cube_distance", "distance_taxicab", "distance_chebyshev"}},
		{"earthdistance", catalog.SQLRoutineLanguage, []string{
			"", "", "", "", "", "", // earth, sec_to_gc, gc_to_sec, ll_to_earth, latitude, longitude
			"geo_distance",
			"", "", // earth_distance, earth_box: parsed RETURN bodies have no source text
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			t.Cleanup(done)
			if tc.name == "earthdistance" {
				extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "cube", ""))
			}
			extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, tc.name, ""))
			view := e.CatalogRead()
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: tc.name}))
			var got []string
			for _, address := range ext.Members {
				p, ok := view.Procedure(address)
				if !ok {
					continue
				}
				language := tc.language
				if tc.name == "earthdistance" && p.Name() == "geo_distance" {
					language = catalog.CRoutineLanguage
				}
				if tc.name == "isn" {
					switch p.Name() {
					case "isneq", "isnne", "isnlt", "isnle", "isngt", "isnge",
						"btean13cmp", "btisbncmp", "btisbn13cmp", "btismncmp",
						"btismn13cmp", "btissncmp", "btissn13cmp", "btupccmp":
						language = catalog.InternalRoutineLanguage
					}
					if p.Display.HasBinary != (language == catalog.CRoutineLanguage) ||
						language == catalog.CRoutineLanguage && p.Display.Binary != "$libdir/isn" ||
						language == catalog.InternalRoutineLanguage && p.Display.Binary != "" {
						t.Fatal("ISN released binary declaration", p)
					}
				}
				if tc.name == "citext" {
					switch p.Name() {
					case "citext_eq", "citext_ne", "citext_lt", "citext_le", "citext_gt", "citext_ge":
						language = catalog.CRoutineLanguage
					case "regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table":
						language = catalog.SQLRoutineLanguage
					case "strpos", "replace", "split_part", "translate":
						symbol, err := extension.ProcedureSymbol(view, p)
						if err != nil || symbol != p.Name() || p.Implementation != extension.StockCitext {
							t.Fatal("invalid citext string display member", p, err)
						}
						language = catalog.SQLRoutineLanguage
						if p.Installed.ArgumentNames != ([4]string{}) || p.Installed.Parallel != 's' || p.Installed.ReturnsSet || p.Installed.Rows != 0 {
							t.Fatal("citext string scalar declaration metadata", p)
						}
					}
				}
				if p.Display.Language != language || p.SecurityDefiner {
					t.Fatal("extension display authority", p)
				}
				got = append(got, p.Display.Source)
				copy := *p
				copy.ObjectName, copy.ObjectNamespace = "renamed", ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				if copy.Display != p.Display {
					t.Fatal("identity mutation changed source")
				}
			}
			want := append([]string(nil), tc.sources...)
			if tc.name == "isn" {
				// The closed release inventory has 28 operand pairs for each
				// predicate, seven checked casts, eight same-type comparators,
				// eight validity/repair pairs and two weak-mode entry points.
				for range 28 {
					want = append(want, "int8eq", "int8ne", "int8lt", "int8le", "int8gt", "int8ge")
				}
				want = append(want, "isbn_cast_from_ean13", "isbn_cast_from_ean13",
					"ismn_cast_from_ean13", "ismn_cast_from_ean13",
					"issn_cast_from_ean13", "issn_cast_from_ean13", "upc_cast_from_ean13")
				for range 8 {
					want = append(want, "btint8cmp", "is_valid", "make_valid")
				}
				want = append(want, "weak_input_status", "accept_weak_input")
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("complete released member bag", got, want)
			}
		})
	}
}
