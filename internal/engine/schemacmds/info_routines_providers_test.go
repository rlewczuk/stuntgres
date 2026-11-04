package schemacmds_test

import (
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
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
	for _, want := range []struct {
		oid        ids.OID
		name       string
		result     ids.OID
		returnsSet bool
		aclEntries int
		signature  catalog.ProcedureSignature
	}{
		{1136, "pg_stat_get_wal", 2249, false, 0, ""},
		{2306, "pg_stat_get_slru", 2249, true, 0, ""},
		{2769, "pg_stat_get_checkpointer_num_timed", 20, false, 0, ""},
		{2770, "pg_stat_get_checkpointer_num_requested", 20, false, 0, ""},
		{2771, "pg_stat_get_checkpointer_buffers_written", 20, false, 0, ""},
		{2772, "pg_stat_get_bgwriter_buf_written_clean", 20, false, 0, ""},
		{2773, "pg_stat_get_bgwriter_maxwritten_clean", 20, false, 0, ""},
		{2859, "pg_stat_get_buf_alloc", 20, false, 0, ""},
		{3046, "pg_stat_get_xact_function_calls", 20, false, 0, "26"},
		{3047, "pg_stat_get_xact_function_total_time", 701, false, 0, "26"},
		{3048, "pg_stat_get_xact_function_self_time", 701, false, 0, "26"},
		{2978, "pg_stat_get_function_calls", 20, false, 0, "26"},
		{2979, "pg_stat_get_function_total_time", 701, false, 0, "26"},
		{2980, "pg_stat_get_function_self_time", 701, false, 0, "26"},
		{3075, "pg_stat_get_bgwriter_stat_reset_time", 1184, false, 0, ""},
		{3160, "pg_stat_get_checkpointer_write_time", 701, false, 0, ""},
		{3161, "pg_stat_get_checkpointer_sync_time", 701, false, 0, ""},
		{3195, "pg_stat_get_archiver", 2249, false, 0, ""},
		{3329, "pg_show_all_file_settings", 2249, true, 1, ""},
		{3400, "pg_config", 2249, true, 1, ""},
		{3781, "pg_get_replication_slots", 2249, true, 0, ""},
		{4100, "pg_get_shmem_allocations_numa", 2249, true, 2, ""},
		{5052, "pg_get_shmem_allocations", 2249, true, 2, ""},
		{6014, "pg_show_replication_origin_status", 2249, true, 1, ""},
		{6118, "pg_stat_get_subscription", 2249, true, 0, "26"},
		{6250, "pg_ident_file_mappings", 2249, true, 1, ""},
		{6314, "pg_stat_get_checkpointer_stat_reset_time", 1184, false, 0, ""},
		{6327, "pg_stat_get_checkpointer_restartpoints_timed", 20, false, 0, ""},
		{6328, "pg_stat_get_checkpointer_restartpoints_requested", 20, false, 0, ""},
		{6329, "pg_stat_get_checkpointer_restartpoints_performed", 20, false, 0, ""},
		{6366, "pg_stat_get_checkpointer_slru_written", 20, false, 0, ""},
		{6377, "pg_stat_get_checkpointer_num_performed", 20, false, 0, ""},
		{6399, "pg_get_aios", 2249, true, 2, ""},
	} {
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: want.oid})
		if seen[want.oid] || p.Builtin == nil || p.Builtin != routine.BuiltinRoutine(want.oid) || p.Builtin.Providers != 0 || p.Builtin.ReturnsSet != want.returnsSet || p.ReturnsSet() != want.returnsSet || p.Name() != want.name || p.RoutineKind != catalog.FunctionRoutine || p.Result != want.result || p.Signature != want.signature || !p.Display.Valid() || p.SecurityDefiner {
			t.Fatal("incomplete metadata-only backing declaration", p, want)
		}
		owner := e.Roles().LookupRef("postgres")
		if p.Owner != owner || p.ACL.Explicit != (want.aclEntries != 0) || len(p.ACL.Entries) != want.aclEntries {
			t.Fatal("metadata-only backing default authority", p, want)
		}
		if want.aclEntries != 0 && p.ACL.Entries[0] != (authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}) {
			t.Fatal("metadata-only backing owner EXECUTE", p)
		}
		if want.aclEntries == 2 && p.ACL.Entries[1] != (authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: e.Roles().LookupRef("pg_read_all_stats")}, Privileges: authorization.ExecutePrivilege}) {
			t.Fatal("metadata-only backing stats EXECUTE", p)
		}
		if want.result != 2249 {
			volatility := expr.Stable
			if want.oid >= 3046 && want.oid <= 3048 {
				volatility = expr.Volatile
			}
			if !p.Strict || p.Volatility != volatility || p.Builtin.Parallel != 'r' || p.OutputDeclaration() != nil || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.CostEstimate() != 1 || p.RowEstimate() != 0 || p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.name}) {
				t.Fatal("released zero-input scalar statistics declaration", p)
			}
		}
		if want.oid == 3195 {
			if p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.name}) {
				t.Fatal("released scalar archiver declaration", p)
			}
			outputs := p.OutputDeclaration()
			if outputs == nil || outputs.Len() != 7 || outputs.Inputs() != 0 {
				t.Fatal("released archiver OUT-only signature", outputs)
			}
			for i, field := range [...]struct {
				name string
				typ  ids.OID
			}{
				{"archived_count", 20},
				{"last_archived_wal", 25},
				{"last_archived_time", 1184},
				{"failed_count", 20},
				{"last_failed_wal", 25},
				{"last_failed_time", 1184},
				{"stats_reset", 1184},
			} {
				if outputs.Name(i) != field.name || outputs.Type(i) != field.typ {
					t.Fatal("released archiver OUT field", i, outputs.Name(i), outputs.Type(i), field)
				}
			}
		}
		if want.oid == 6118 {
			if p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.RowEstimate() != 10 || p.CostEstimate() != 1 || p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.name}) {
				t.Fatal("released subscription backing declaration", p)
			}
			outputs := p.OutputDeclaration()
			if outputs == nil || outputs.Inputs() != 1 || outputs.Len() != 10 || outputs.Name(0) != "subid" || p.InputNames != nil {
				t.Fatal("released subscription IN/OUT signature", outputs)
			}
			for i, field := range [...]struct {
				name string
				typ  ids.OID
			}{
				{"subid", 26}, {"relid", 26}, {"pid", 23}, {"leader_pid", 23},
				{"received_lsn", 3220}, {"last_msg_send_time", 1184}, {"last_msg_receipt_time", 1184},
				{"latest_end_lsn", 3220}, {"latest_end_time", 1184}, {"worker_type", 25},
			} {
				if outputs.Name(i+1) != field.name || outputs.Type(i) != field.typ {
					t.Fatal("released subscription OUT field", i, outputs.Name(i+1), outputs.Type(i), field)
				}
			}
		}
		if want.oid == 6014 {
			if p.Strict || p.Volatility != expr.Volatile || p.Builtin.Parallel != 'r' || p.RowEstimate() != 100 || p.CostEstimate() != 1 || p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.name}) {
				t.Fatal("released origin status backing declaration", p)
			}
			outputs := p.OutputDeclaration()
			if outputs == nil || outputs.Inputs() != 0 || outputs.Len() != 4 || p.InputNames != nil {
				t.Fatal("released origin status OUT-only signature", outputs)
			}
			for i, field := range [...]struct {
				name string
				typ  ids.OID
			}{{"local_id", 26}, {"external_id", 25}, {"remote_lsn", 3220}, {"local_lsn", 3220}} {
				if outputs.Name(i) != field.name || outputs.Type(i) != field.typ {
					t.Fatal("released origin status OUT field", i, outputs.Name(i), outputs.Type(i), field)
				}
			}
		}
		if want.oid == 3781 {
			if p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 's' || p.RowEstimate() != 10 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 {
				t.Fatal("released slot inventory backing declaration", p)
			}
			outputs := p.OutputDeclaration()
			if outputs == nil || outputs.Inputs() != 0 || outputs.Len() != 20 || p.InputNames != nil {
				t.Fatal("released slot inventory OUT-only signature", outputs)
			}
			for i, field := range [...]struct {
				name string
				typ  ids.OID
			}{
				{"slot_name", 19}, {"plugin", 19}, {"slot_type", 25}, {"datoid", 26},
				{"temporary", 16}, {"active", 16}, {"active_pid", 23}, {"xmin", 28},
				{"catalog_xmin", 28}, {"restart_lsn", 3220}, {"confirmed_flush_lsn", 3220},
				{"wal_status", 25}, {"safe_wal_size", 20}, {"two_phase", 16}, {"two_phase_at", 3220},
				{"inactive_since", 1184}, {"conflicting", 16}, {"invalidation_reason", 25},
				{"failover", 16}, {"synced", 16},
			} {
				if outputs.Name(i) != field.name || outputs.Type(i) != field.typ {
					t.Fatal("released slot inventory OUT field", i, outputs.Name(i), outputs.Type(i), field)
				}
			}
		}
		seen[want.oid] = true
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
		1136: {Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_wal"},
		2282: {Language: catalog.InternalRoutineLanguage, Source: "pg_get_backend_memory_contexts"},
		2306: {Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_slru"},
		3195: {Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_archiver"},
		3329: {Language: catalog.InternalRoutineLanguage, Source: "show_all_file_settings"},
		6250: {Language: catalog.InternalRoutineLanguage, Source: "pg_ident_file_mappings"},
		6118: {Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_subscription"},
		6014: {Language: catalog.InternalRoutineLanguage, Source: "pg_show_replication_origin_status"},
		3781: {Language: catalog.InternalRoutineLanguage, Source: "pg_get_replication_slots"},
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
		{"intagg", catalog.InternalRoutineLanguage, []string{"array_agg_transfn", "array_agg_finalfn", "aggregate_dummy", "array_unnest"}},
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
