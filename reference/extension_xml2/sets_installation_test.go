package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xml2SetNames = "'xpath_list','xpath_nodeset'"

func TestReferenceXML2SetsInstallation(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		t.Run("Inventory", func(t *testing.T) { xml2Inventory(t, p, b, c) })
		q := "SELECT p.proname,p.pronargs,p.proargtypes::text AS args,p.prorettype::bigint AS result_oid,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,l.lanname,p.prosrc,p.probin,p.prosqlbody IS NOT NULL AS structural,p.procost,p.pronargdefaults,p.proargdefaults IS NULL AS no_defaults,p.proargnames IS NULL AS unnamed,p.provariadic::bigint AS variadic,p.proretset,p.prosecdef,p.proconfig IS NULL AS no_config FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='public' AND p.proname IN (" + xml2SetNames + ") ORDER BY p.proname,p.pronargs"
		rows := [][]any{
			{"xpath_list", int64(2), "25 25", int64(25), true, "i", "s", "sql", "", nil, true, float64(100), int64(0), true, true, int64(0), false, false, true},
			{"xpath_list", int64(3), "25 25 25", int64(25), true, "i", "s", "c", "xpath_list", "$libdir/pgxml", false, float64(1), int64(0), true, true, int64(0), false, false, true},
			{"xpath_nodeset", int64(2), "25 25", int64(25), true, "i", "s", "sql", "", nil, true, float64(100), int64(0), true, true, int64(0), false, false, true},
			{"xpath_nodeset", int64(3), "25 25 25", int64(25), true, "i", "s", "sql", "", nil, true, float64(100), int64(0), true, true, int64(0), false, false, true},
			{"xpath_nodeset", int64(4), "25 25 25 25", int64(25), true, "i", "s", "c", "xpath_nodeset", "$libdir/pgxml", false, float64(1), int64(0), true, true, int64(0), false, false, true},
		}
		t.Run("Properties", func(t *testing.T) {
			xml2Both(t, p, b, c, fixture.Select(q,
				[]string{"proname", "pronargs", "args", "result_oid", "proisstrict", "volatility", "parallel", "lanname", "prosrc", "probin", "structural", "procost", "pronargdefaults", "no_defaults", "unnamed", "variadic", "proretset", "prosecdef", "no_config"},
				[]string{"NAME", "INT2", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "NAME", "TEXT", "TEXT", "BOOL", "FLOAT4", "INT2", "BOOL", "BOOL", "INT8", "BOOL", "BOOL", "BOOL"}, rows))
		})
		// Stable structural presence is asserted above. Never compare opaque
		// prosqlbody node trees or implementation-assigned OIDs across backends.
		xml2SetDependencies(t, p, b, c, "xpath_nodeset", "xpath_list")
	})
}

func xml2SetDependencies(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, nodes, list string) {
	t.Helper()
	q := "SELECT outerp.proname,outerp.pronargs,innerp.proname AS inner_name,innerp.pronargs AS inner_arity,d.deptype FROM pg_depend d JOIN pg_proc outerp ON d.classid=1255 AND d.objid=outerp.oid JOIN pg_proc innerp ON d.refclassid=1255 AND d.refobjid=innerp.oid JOIN pg_namespace n ON n.oid=outerp.pronamespace WHERE n.nspname='public' AND outerp.proname IN (" + xml2SetNames + ") AND d.deptype='n' ORDER BY outerp.proname,outerp.pronargs,innerp.proname,innerp.pronargs"
	xml2Both(t, p, b, c, fixture.Select(q, []string{"proname", "pronargs", "inner_name", "inner_arity", "deptype"}, []string{"NAME", "INT2", "NAME", "INT2", "CHAR"}, [][]any{
		{"xpath_list", int64(2), list, int64(3), "n"},
		{"xpath_nodeset", int64(2), nodes, int64(4), "n"},
		{"xpath_nodeset", int64(3), nodes, int64(4), "n"},
	}))
}
