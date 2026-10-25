package engine

import (
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestInfoRoutinesBuiltinCompleteness(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	seen := make(map[ids.OID]bool)
	for _, declaration := range routine.BuiltinRoutineDeclarations {
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID})
		if seen[p.Address().ObjectID] || p.Builtin == nil || !p.Display.Valid() || p.Display.SecurityDefiner || p.Display.Source == "see system_functions.sql" {
			t.Fatal("incomplete real builtin declaration", p)
		}
		seen[p.Address().ObjectID] = true
	}
	for _, d := range view.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok {
			if !p.Display.Valid() || p.Display.SecurityDefiner {
				t.Fatal("uninitialized real provider", p)
			}
			if p.Builtin != nil && !seen[p.Address().ObjectID] {
				t.Fatal("unowned builtin row", p)
			}
			if p.PLHandler != catalog.NoPLHandler {
				name, _, _ := p.PLHandler.Declaration()
				if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Strict != (p.PLHandler != catalog.PLCallHandler) || p.Language != (ids.ObjectAddress{}) {
					t.Fatal("PL canonical declaration", p)
				}
			}
			if p.Conversion.Name() != "" && p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: p.Conversion.Name()}) {
				t.Fatal("conversion declaration", p)
			}
		}
	}
	for oid, want := range map[ids.OID]catalog.RoutineDisplay{
		1397: {Language: catalog.InternalRoutineLanguage, Source: "int4abs"},
		3100: {Language: catalog.InternalRoutineLanguage, Source: "window_row_number"},
		2147: {Language: catalog.InternalRoutineLanguage, Source: "aggregate_dummy"},
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
		{"pgcrypto", catalog.CRoutineLanguage, []string{"pg_digest", "pg_digest", "pg_hmac", "pg_hmac", "pg_crypt", "pg_gen_salt", "pg_gen_salt_rounds", "pg_encrypt", "pg_decrypt", "pg_encrypt_iv", "pg_decrypt_iv", "pg_random_bytes", "pg_random_uuid", "pg_check_fipsmode"}},
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
		{"intarray", catalog.CRoutineLanguage, []string{"bqarr_in", "bqarr_out"}},
		{"isn", catalog.CRoutineLanguage, []string{"ean13_in", "ean13_out", "isbn_in", "isn_out", "isbn_in", "ean13_out", "ismn_in", "isn_out", "ismn_in", "ean13_out"}},
		{"intagg", catalog.InternalRoutineLanguage, []string{"aggregate_dummy", "array_unnest"}},
		{"pgrowlocks", catalog.CRoutineLanguage, []string{"pgrowlocks"}},
		{"cube", catalog.CRoutineLanguage, []string{"cube_in", "cube_out", "cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge", "cube_cmp", "cube_f8", "cube_f8_f8", "cube_a_f8", "cube_a_f8_f8", "cube_c_f8", "cube_c_f8_f8", "cube_dim", "cube_is_point", "cube_ll_coord", "cube_ur_coord", "cube_coord", "cube_coord_llur", "cube_overlap", "cube_contains", "cube_contained", "cube_distance", "distance_taxicab", "distance_chebyshev"}},
		{"earthdistance", catalog.SQLRoutineLanguage, []string{"", "", "", "", "", "", "geo_distance"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
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
				if tc.name == "citext" {
					switch p.Name() {
					case "citext_eq", "citext_ne", "citext_lt", "citext_le", "citext_gt", "citext_ge":
						language = catalog.CRoutineLanguage
					case "regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table":
						language = catalog.SQLRoutineLanguage
     case "strpos", "replace", "split_part", "translate":
      symbol,err:=extension.ProcedureSymbol(view,p)
      if err!=nil||symbol!=p.Name()||p.Implementation!=extension.StockCitext {
       t.Fatal("invalid citext string display member",p,err)
      }
      language=catalog.SQLRoutineLanguage
      if p.Installed.ArgumentNames!=([4]string{})||p.Installed.Parallel!='s'||p.Installed.ReturnsSet||p.Installed.Rows!=0 {
       t.Fatal("citext string scalar declaration metadata",p)
      }
					}
				}
				if p.Display.Language != language || p.Display.SecurityDefiner {
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
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("complete released member bag", got, want)
			}
		})
	}
}
