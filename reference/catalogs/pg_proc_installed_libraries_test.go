package reference_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independent REL_18_6 contrib installation/upgrade SQL and each module's
// .control MODULE_PATHNAME. One distinguishing declaration for every changed
// C-library owner; cube's codec and spatial constructors are separate owners.
func TestReferencePGProcInstalledLibraries(t *testing.T) {
	for _, tc := range []struct{ extension, signature, source, library, result string }{
		{"amcheck", "bt_index_check(regclass)", "bt_index_check", "amcheck", "void"},
		{"citext", "citext_eq(pp_inst_lib.citext,pp_inst_lib.citext)", "citext_eq", "citext", "boolean"},
		{"cube", "cube_in(cstring)", "cube_in", "cube", "pp_inst_lib.cube"},
		{"cube", "cube_dim(pp_inst_lib.cube)", "cube_dim", "cube", "integer"},
		{"earthdistance", "geo_distance(point,point)", "geo_distance", "earthdistance", "double precision"},
		{"fuzzystrmatch", "soundex(text)", "soundex", "fuzzystrmatch", "text"},
		{"hstore", "hstore_in(cstring)", "hstore_in", "hstore", "pp_inst_lib.hstore"},
		{"intarray", "bqarr_in(cstring)", "bqarr_in", "_int", "pp_inst_lib.query_int"},
		{"isn", "ean13_in(cstring)", "ean13_in", "isn", "pp_inst_lib.ean13"},
		{"ltree", "ltree_in(cstring)", "ltree_in", "ltree", "pp_inst_lib.ltree"},
		{"pageinspect", "brin_page_type(bytea)", "brin_page_type", "pageinspect", "text"},
		{"pgcrypto", "digest(text,text)", "pg_digest", "pgcrypto", "bytea"},
		{"pgrowlocks", "pgrowlocks(text)", "pgrowlocks", "pgrowlocks", "record"},
		{"seg", "seg_in(cstring)", "seg_in", "seg", "pp_inst_lib.seg"},
		{"tablefunc", "normal_rand(integer,double precision,double precision)", "normal_rand", "tablefunc", "double precision"},
		{"unaccent", "unaccent(text)", "unaccent_dict", "unaccent", "text"},
		{"uuid-ossp", "uuid_generate_v4()", "uuid_generate_v4", "uuid-ossp", "uuid"},
	} {
		t.Run(tc.extension+"/"+tc.signature, func(t *testing.T) {
			p := fixture.NewScenario(t)
			native := p.Backends()[0]
			cleanup := func(t *testing.T, x fixture.Executor) {
				t.Helper()
				for _, q := range []string{"DROP EXTENSION IF EXISTS " + pgProcIdent(tc.extension) + " CASCADE", "DROP EXTENSION IF EXISTS cube CASCADE", "DROP SCHEMA IF EXISTS pp_inst_lib CASCADE"} {
					if _, err := x.ExecContext(context.Background(), q); err != nil {
						t.Error(q, err)
					}
				}
			}
			defer cleanup(t, native.DB)
			pgProcExec(t, p, native, native.DB, "CREATE SCHEMA pp_inst_lib")
			if tc.extension == "earthdistance" {
				pgProcExec(t, p, native, native.DB, "CREATE EXTENSION cube SCHEMA pp_inst_lib")
			}
			pgProcExec(t, p, native, native.DB, "CREATE EXTENSION "+pgProcIdent(tc.extension)+" SCHEMA pp_inst_lib")
			dump := fixture.ViewDump(t, p)
			if len(dump) == 0 {
				t.Fatal("empty installed library Dump")
			}
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var x fixture.Executor = b.DB
					if b.PG {
						c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
						x = c
						defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
						defer cleanup(t, c)
						if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
							t.Fatal("PG rejected actual installed library Dump", err)
						}
					} else {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
							t.Fatal(err)
						}
					}
					pgProcBoth(t, p, b, x, "SELECT p.proname,l.lanname,p.prosrc,p.probin,p.prosqlbody IS NULL AS no_body,p.prorettype=$2::regtype::oid AS result,p.prokind FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::regprocedure::oid", []string{"proname", "lanname", "prosrc", "probin", "no_body", "result", "prokind"}, []string{"NAME", "NAME", "TEXT", "TEXT", "BOOL", "BOOL", "CHAR"}, [][]any{{tc.signature[:strings.IndexByte(tc.signature, '(')], "c", tc.source, "$libdir/" + tc.library, true, true, "f"}}, "pp_inst_lib."+tc.signature, tc.result)
				})
			}
		})
	}
}
