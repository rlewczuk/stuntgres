package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationInventory(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		q := `SELECT p.proname::text AS routine,p.pronargs::integer AS nargs,COALESCE(a.typname::text,'') AS argument,r.typname::text AS result,l.lanname::text AS language,p.prosrc,p.probin,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proretset,p.prosecdef,p.pronargdefaults::integer AS defaults,p.provariadic=0 AS nonvariadic,p.proargnames IS NULL AS unnamed,p.proallargtypes IS NULL AS no_out,p.prokind::text AS kind FROM pg_proc p LEFT JOIN pg_type a ON a.oid=p.proargtypes[0] JOIN pg_type r ON r.oid=p.prorettype JOIN pg_language l ON l.oid=p.prolang WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('is_valid','make_valid','isn_weak')`
		rows := [][]any{}
		for _, role := range isnConversionRoles {
			for _, name := range []string{"is_valid", "make_valid"} {
				result := "bool"
				if name == "make_valid" {
					result = role.name
				}
				rows = append(rows, []any{name, int64(1), role.name, result, "c", name, "$libdir/isn", true, "i", "s", false, false, int64(0), true, true, true, "f"})
			}
		}
		rows = append(rows,
			[]any{"isn_weak", int64(0), "", "bool", "c", "weak_input_status", "$libdir/isn", true, "s", "s", false, false, int64(0), true, true, true, "f"},
			[]any{"isn_weak", int64(1), "bool", "bool", "c", "accept_weak_input", "$libdir/isn", true, "v", "u", false, false, int64(0), true, true, true, "f"})
		s := fixture.Select(q, []string{"routine", "nargs", "argument", "result", "language", "prosrc", "probin", "proisstrict", "volatility", "parallel", "proretset", "prosecdef", "defaults", "nonvariadic", "unnamed", "no_out", "kind"}, []string{"TEXT", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "INT4", "BOOL", "BOOL", "BOOL", "TEXT"}, rows)
		s.Bag = true
		isnConversionRoutes(t, p, b, c, s)
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='isn') AND d.deptype='e' AND p.pronamespace='public'::regnamespace AND p.proname IN ('is_valid','make_valid','isn_weak')`, 18))
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_operator o JOIN pg_proc p ON p.oid=o.oprcode WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('is_valid','make_valid','isn_weak')`, 0))
		// Relocation changes namespace, never identities of the established codec,
		// comparison, conversion or new validation prefix/suffix declarations.
		identities := map[string]int64{}
		for _, sig := range []string{"ean13_in(cstring)", "isneq(ean13,ean13)", "isbn(ean13)", "is_valid(ean13)", "make_valid(upc)", "isn_weak()", "isn_weak(boolean)"} {
			identities[sig] = fixture.OwnershipID(t, p, c, "SELECT '"+sig+"'::regprocedure::bigint")
		}
		run(fixture.Exec("CREATE SCHEMA eo_new", 0))
		run(fixture.Exec("ALTER EXTENSION isn SET SCHEMA eo_new", 0))
		run(fixture.Exec("SET search_path=eo_new,pg_catalog", 0))
		for sig, id := range identities {
			if fixture.OwnershipID(t, p, c, "SELECT '"+sig+"'::regprocedure::bigint") != id {
				t.Fatal("relocation replaced installed identity", sig)
			}
		}
	})
}
