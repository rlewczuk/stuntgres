package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesProvidersExtensions(t *testing.T) {
	for _, tc := range []struct {
		extension, version, name, inputs, kind, data, udtSchema, udt, lang, source string
		immutable, strict                                                          bool
	}{
		{"pgcrypto", "1.4", "digest", "text,text", "FUNCTION", "bytea", "pg_catalog", "bytea", "C", "pg_digest", true, true},
		{"uuid-ossp", "1.1", "uuid_generate_v4", "", "FUNCTION", "uuid", "pg_catalog", "uuid", "C", "uuid_generate_v4", false, true},
		{"fuzzystrmatch", "1.1", "levenshtein", "text,text,integer,integer,integer", "FUNCTION", "integer", "pg_catalog", "int4", "C", "levenshtein_with_costs", true, true},
		{"fuzzystrmatch", "1.2", "daitch_mokotoff", "text", "FUNCTION", "ARRAY", "pg_catalog", "_text", "C", "daitch_mokotoff", true, true},
		{"unaccent", "1.1", "unaccent", "text", "FUNCTION", "text", "pg_catalog", "text", "C", "unaccent_dict", false, true},
		{"unaccent", "1.1", "unaccent_init", "internal", "FUNCTION", "internal", "pg_catalog", "internal", "C", "unaccent_init", false, false},
		{"unaccent", "1.1", "unaccent_lexize", "internal,internal,internal,internal", "FUNCTION", "internal", "pg_catalog", "internal", "C", "unaccent_lexize", false, false},
		{"citext", "1.8", "citextin", "cstring", "FUNCTION", "USER-DEFINED", "public", "citext", "INTERNAL", "textin", true, true},
		{"citext", "1.8", "citextout", "citext", "FUNCTION", "cstring", "pg_catalog", "cstring", "INTERNAL", "textout", true, true},
		{"intarray", "1.5", "bqarr_in", "cstring", "FUNCTION", "USER-DEFINED", "public", "query_int", "C", "bqarr_in", true, true},
		{"isn", "1.3", "ean13_in", "cstring", "FUNCTION", "USER-DEFINED", "public", "ean13", "C", "ean13_in", true, true},
		{"pgrowlocks", "1.2", "pgrowlocks", "text", "FUNCTION", "record", "pg_catalog", "record", "C", "pgrowlocks", false, true},
		{"intagg", "1.1", "int_array_aggregate", "integer", "", "ARRAY", "pg_catalog", "_int4", "INTERNAL", "aggregate_dummy", true, false},
		{"intagg", "1.1", "int_array_enum", "integer[]", "FUNCTION", "integer", "pg_catalog", "int4", "INTERNAL", "array_unnest", true, true},
		{"cube", "1.5", "cube", "double precision", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_f8", true, true},
		{"cube", "1.5", "cube", "double precision,double precision", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_f8_f8", true, true},
		{"cube", "1.5", "cube", "double precision[]", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_a_f8", true, true},
		{"cube", "1.5", "cube", "double precision[],double precision[]", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_a_f8_f8", true, true},
		{"cube", "1.5", "cube", "cube,double precision", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_c_f8", true, true},
		{"cube", "1.5", "cube", "cube,double precision,double precision", "FUNCTION", "USER-DEFINED", "public", "cube", "C", "cube_c_f8_f8", true, true},
		{"earthdistance", "1.2", "earth", "", "FUNCTION", "double precision", "pg_catalog", "float8", "SQL", "", true, false},
		{"earthdistance", "1.2", "sec_to_gc", "double precision", "FUNCTION", "double precision", "pg_catalog", "float8", "SQL", "", true, true},
		{"earthdistance", "1.2", "ll_to_earth", "double precision,double precision", "FUNCTION", "USER-DEFINED", "public", "earth", "SQL", "", true, true},
		{"earthdistance", "1.2", "geo_distance", "point,point", "FUNCTION", "double precision", "pg_catalog", "float8", "C", "geo_distance", true, true},
	} {
		t.Run(tc.extension+"_"+tc.name+"_"+tc.inputs, func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				// Register cleanup before install; dependencies are removed after their user.
				defer func() {
					_, _ = c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS \""+tc.extension+"\" CASCADE")
					if tc.extension == "earthdistance" {
						_, _ = c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS cube CASCADE")
					}
				}()
				if tc.extension == "earthdistance" {
					run(fixture.Exec("CREATE EXTENSION cube", 0))
				}
				run(fixture.Exec("CREATE EXTENSION \""+tc.extension+"\" VERSION '"+tc.version+"'", 0))
				id := rsuIdentity(t, p, c, "public", tc.name, tc.inputs)
				var external any
				if tc.lang == "C" {
					external = tc.source
				}
				row := irRow(db, "public", tc.name, id, tc.kind, tc.data, tc.udtSchema, tc.udt, tc.lang, tc.source, external, tc.immutable, tc.strict)
				where := "specific_schema='public' AND specific_name='" + id + "'"
				irCheck(t, p, b, c, irProjection(b.PG, where, [][]any{row}))
				if tc.extension == "unaccent" && tc.name == "unaccent" {
					run(fixture.Exec("ALTER FUNCTION unaccent(text) RENAME TO ir_original_symbol", 0))
					run(fixture.Exec("CREATE SCHEMA ir_moved", 0))
					run(fixture.Exec("ALTER FUNCTION ir_original_symbol(text) SET SCHEMA ir_moved", 0))
					row[1], row[4], row[5] = "ir_moved", "ir_moved", "ir_original_symbol"
					row[2] = rsuIdentity(t, p, c, "ir_moved", "ir_original_symbol", "text")
					irCheck(t, p, b, c, irProjection(b.PG, "routine_schema='ir_moved' AND routine_name='ir_original_symbol'", [][]any{row}))
				}
				run(fixture.Exec("DROP EXTENSION \""+tc.extension+"\" CASCADE", 0))
				irCheck(t, p, b, c, irProjection(b.PG, where, nil))
			})
		})
	}
}
