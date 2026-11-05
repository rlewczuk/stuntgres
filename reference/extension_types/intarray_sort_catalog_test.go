package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySortCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) []int64 {
	t.Helper()
	// Preserve all previously selected provider/SET/SLICE declarations without
	// equating native's progressive inventory with upstream's whole extension.
	intarraySliceCatalog(t, p, b, c, owner)
	var ids []int64
	var objects []string
	for _, tc := range intarraySortCases {
		object := "('ia_provider." + tc.name + "(" + strings.Join(tc.types, ",") + ")'::regprocedure)::oid"
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT "+object).Scan(&id); err != nil || id <= 0 {
			t.Fatalf("sort identity %s=%d: %v", object, id, err)
		}
		for _, previous := range ids {
			if id == previous {
				t.Fatalf("sort overload identities alias %d", id)
			}
		}
		ids = append(ids, id)
		objects = append(objects, object)
		args := "1007"
		if len(tc.types) > 1 {
			args += " 25"
		}
		q := "SELECT proname,proargtypes::text AS args,prorettype::text AS result,pronargs,prokind,proretset,proisstrict,provolatile,proparallel,l.lanname,prosrc,probin,r.rolname,pronamespace='ia_provider'::regnamespace AS namespace,provariadic=0::oid AS nonvariadic,pronargdefaults,proargnames IS NULL AS unnamed,proallargtypes IS NULL AS inputs_only,proargmodes IS NULL AS no_modes FROM (SELECT * FROM pg_proc WHERE oid=" + object + ") p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner"
		intarrayBoth(t, p, b, c, fixture.Select(q, []string{"proname", "args", "result", "pronargs", "prokind", "proretset", "proisstrict", "provolatile", "proparallel", "lanname", "prosrc", "probin", "rolname", "namespace", "nonvariadic", "pronargdefaults", "unnamed", "inputs_only", "no_modes"}, []string{"NAME", "TEXT", "TEXT", "INT2", "CHAR", "BOOL", "BOOL", "CHAR", "CHAR", "NAME", "TEXT", "TEXT", "NAME", "BOOL", "BOOL", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.name, args, "1007", int64(len(tc.types)), "f", false, true, "i", "s", "c", tc.name, "$libdir/_int", owner, true, true, int64(0), true, true, true}}))
		q = "SELECT refobjid=(SELECT oid FROM pg_extension WHERE extname='intarray') AS extension FROM pg_depend WHERE classid=1255 AND objid=" + object + " AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'"
		intarrayBoth(t, p, b, c, fixture.Select(q, []string{"extension"}, []string{"BOOL"}, [][]any{{true}}))
	}
	intarrayBoth(t, p, b, c, fixture.Select("SELECT proname,proargtypes::text AS args FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace AND proname IN ('sort','sort_asc','sort_desc','uniq') ORDER BY proname,args", []string{"proname", "args"}, []string{"NAME", "TEXT"}, [][]any{{"sort", "1007"}, {"sort", "1007 25"}, {"sort_asc", "1007"}, {"sort_desc", "1007"}, {"uniq", "1007"}}))
	intarrayBoth(t, p, b, c, fixture.Select("SELECT oprname FROM pg_operator WHERE oprcode::oid IN ("+strings.Join(objects, ",")+") ORDER BY oprname", []string{"oprname"}, []string{"NAME"}, [][]any{}))
	return ids
}

func TestReferenceIntarraySortCatalog(t *testing.T) {
	t.Run("declarations", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			if ids := intarraySortCatalog(t, p, b, c, intarrayFixtureOwner(b)); len(ids) != 5 {
				t.Fatalf("sort declarations %v", ids)
			}
		})
	})
	for _, tc := range []struct {
		label, name, args, script string
		line                      int
	}{
		{"sort_unary", "sort", "integer[]", "sort(_int4)", 248},
		{"sort_binary", "sort", "integer[],text", "sort(_int4, text)", 243},
		{"sort_asc", "sort_asc", "integer[]", "sort_asc(_int4)", 253},
		{"sort_desc", "sort_desc", "integer[]", "sort_desc(_int4)", 258},
		{"uniq", "uniq", "integer[]", "uniq(_int4)", 263},
	} {
		t.Run("collision_"+tc.label, func(t *testing.T) {
			intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE SCHEMA ia_provider", 0))
				run(fixture.Exec("CREATE TYPE ia_provider.sort_marker AS (v integer)", 0))
				base, array := intarrayOID(t, p, c, "ia_provider.sort_marker"), intarrayOID(t, p, c, "ia_provider._sort_marker")
				run(fixture.Exec("CREATE FUNCTION ia_provider."+tc.name+"("+tc.args+") RETURNS integer[] LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT $1'", 0))
				object := "('ia_provider." + tc.name + "(" + tc.args + ")'::regprocedure)::oid"
				var before int64
				if err := c.QueryRowContext(p.Ctx, "SELECT "+object).Scan(&before); err != nil {
					t.Fatal(err)
				}
				want := fixture.ControlsReferenceDiagnostic("42723", "function \""+tc.name+"\" already exists with same argument types")
				// Complete script Context and near-line values confirmed
				// against the pinned PostgreSQL 18.6 oracle.
				statement := "CREATE FUNCTION " + tc.script + "\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE"
				want.Context = "SQL statement \"" + statement + "\"\nextension script file \"intarray--1.2.sql\", near line " + strconv.Itoa(tc.line)
				for _, prepared := range []bool{false, true} {
					intarrayError(t, p, b, c, "CREATE EXTENSION intarray SCHEMA ia_provider", prepared, "execute", want)
					intarrayBoth(t, p, b, c, fixture.Select("SELECT "+object+"=$1::oid AS original", []string{"original"}, []string{"BOOL"}, [][]any{{true}}, before))
					intarrayBoth(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
					intarrayBoth(t, p, b, c, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname IN ('query_int','_query_int','intbig_gkey','_intbig_gkey') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
					intarrayBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{tc.name}}))
					intarrayBoth(t, p, b, c, fixture.Select("SELECT oprname FROM pg_operator WHERE oprnamespace='ia_provider'::regnamespace ORDER BY oprname", []string{"oprname"}, []string{"NAME"}, [][]any{}))
					args := []string{"'{3,1,1,2}'::integer[]"}
					if tc.args == "integer[],text" {
						args = append(args, "'DESC'::text")
					}
					intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT ia_provider."+tc.name+"("+strings.Join(args, ",")+") AS v", intarraySortA))
					if base != intarrayOID(t, p, c, "ia_provider.sort_marker") || array != intarrayOID(t, p, c, "ia_provider._sort_marker") {
						t.Fatal("sort collision replaced unrelated type/companion")
					}
				}
			})
		})
	}
}
