package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xml2Names = "'xml_valid','xpath_bool','xpath_list','xpath_nodeset','xpath_number','xpath_string','xpath_table'"

func xml2Inventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xml2Both(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='xml2'",
		[]string{"extversion", "extrelocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"1.2", false}}))
	q := "SELECT p.proname,p.pronargs,p.proargtypes::text AS args,p.prorettype::bigint AS result_oid,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,l.lanname,p.prosrc,p.probin,p.oid>0 AS allocated,p.pronamespace=e.extnamespace AS same_schema,p.proargnames IS NULL AS unnamed,p.proretset FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='xml2' AND p.proname IN (" + xml2Names + ") ORDER BY p.proname,p.pronargs"
	xml2Both(t, p, b, c, fixture.Select(q,
		[]string{"proname", "pronargs", "args", "result_oid", "proisstrict", "volatility", "parallel", "lanname", "prosrc", "probin", "allocated", "same_schema", "unnamed", "proretset"},
		[]string{"NAME", "INT2", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "NAME", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
			{"xml_valid", int64(1), "25", int64(16), true, "s", "s", "internal", "xml_is_well_formed", nil, true, true, true, false},
			{"xpath_bool", int64(2), "25 25", int64(16), true, "i", "s", "c", "xpath_bool", "$libdir/pgxml", true, true, true, false},
			{"xpath_list", int64(2), "25 25", int64(25), true, "i", "s", "sql", "", nil, true, true, true, false},
			{"xpath_list", int64(3), "25 25 25", int64(25), true, "i", "s", "c", "xpath_list", "$libdir/pgxml", true, true, true, false},
			{"xpath_nodeset", int64(2), "25 25", int64(25), true, "i", "s", "sql", "", nil, true, true, true, false},
			{"xpath_nodeset", int64(3), "25 25 25", int64(25), true, "i", "s", "sql", "", nil, true, true, true, false},
			{"xpath_nodeset", int64(4), "25 25 25 25", int64(25), true, "i", "s", "c", "xpath_nodeset", "$libdir/pgxml", true, true, true, false},
			{"xpath_number", int64(2), "25 25", int64(700), true, "i", "s", "c", "xpath_number", "$libdir/pgxml", true, true, true, false},
			{"xpath_string", int64(2), "25 25", int64(25), true, "i", "s", "c", "xpath_string", "$libdir/pgxml", true, true, true, false},
			{"xpath_table", int64(5), "25 25 25 25 25", int64(2249), true, "s", "s", "c", "xpath_table", "$libdir/pgxml", true, true, true, true},
		}))
	xml2Both(t, p, b, c, fixture.Select("SELECT p.proname,p.pronargs,p.provariadic::bigint AS variadic,p.pronargdefaults,p.proretset FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='xml2' AND p.proname IN ("+xml2Names+") ORDER BY p.proname,p.pronargs",
		[]string{"proname", "pronargs", "variadic", "pronargdefaults", "proretset"}, []string{"NAME", "INT2", "INT8", "INT2", "BOOL"}, [][]any{
			{"xml_valid", int64(1), int64(0), int64(0), false},
			{"xpath_bool", int64(2), int64(0), int64(0), false},
			{"xpath_list", int64(2), int64(0), int64(0), false},
			{"xpath_list", int64(3), int64(0), int64(0), false},
			{"xpath_nodeset", int64(2), int64(0), int64(0), false},
			{"xpath_nodeset", int64(3), int64(0), int64(0), false},
			{"xpath_nodeset", int64(4), int64(0), int64(0), false},
			{"xpath_number", int64(2), int64(0), int64(0), false},
			{"xpath_string", int64(2), int64(0), int64(0), false},
			{"xpath_table", int64(5), int64(0), int64(0), true},
		}))
	xml2Both(t, p, b, c, fixture.Select("SELECT p.procost,p.prorows FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='xml2' AND p.proname='xpath_table' AND p.proargtypes::text='25 25 25 25 25'",
		[]string{"procost", "prorows"}, []string{"FLOAT4", "FLOAT4"}, [][]any{{float64(1), float64(1000)}}))
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='xml2'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}))
	}
}

func xml2Consumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE xml2_saved(id integer, v boolean DEFAULT xml_valid('<r/>'), s text DEFAULT xpath_string('<r>12</r>','.'), n real DEFAULT xpath_number('<r>12</r>','.'), b boolean DEFAULT xpath_bool('<r>12</r>','.'), CHECK (b IS NULL OR b))", 0),
		fixture.Exec("INSERT INTO xml2_saved(id) VALUES(1)", 1),
		fixture.Exec("CREATE TABLE xml2_guard(id integer,doc text,path text,CONSTRAINT xml2_guard_xpath CHECK(xpath_string(doc,path) <> ''))", 0),
		fixture.Exec("INSERT INTO xml2_guard VALUES(1,'<r>12</r>','.')", 1),
		// The view below demands every installed expression, nested in ordinary SQL.
		fixture.Exec("CREATE VIEW xml2_values AS SELECT id,xml_valid('<r/>'::text) AS v,coalesce(xpath_string('<r>12</r>','.'),'missing') AS s,CASE WHEN b THEN xpath_number('<r>12</r>','.') ELSE n END AS n,xpath_bool('<r>12</r>','.') AS b FROM xml2_saved", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func xml2Saved(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	for _, table := range []string{"xml2_saved", "xml2_values"} {
		xml2Both(t, p, b, x, fixture.Select("SELECT id,v,s,n,b FROM "+table+" ORDER BY id", []string{"id", "v", "s", "n", "b"}, []string{"INT4", "BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{int64(1), true, "12", float64(12), true}}))
	}
}

func TestReferenceXML2Identity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xml2_probe_user", "xml2_probe_denied", "xml2_provider_owner"})
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
		xml2Install(t, p, b, c)
		xml2Inventory(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='xml2'", []string{"extname"}, []string{"NAME"}, nil))
		xml2Install(t, p, b, c)
		xml2Inventory(t, p, b, c)
		xml2Consumers(t, p, b, c)
		xml2Saved(t, p, b, c)
		xml2Guard(t, p, b, c, "public", "xpath_string")
		xml2ACLSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION xpath_string(text,text) RENAME TO xml2_text", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml2_text('<r>12</r>','.') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
		xml2Saved(t, p, b, c)
		xml2Guard(t, p, b, c, "public", "xml2_text")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER EXTENSION xml2 DROP FUNCTION xml2_text(text,text)", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml2_text('<r>12</r>','.') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
		xml2Saved(t, p, b, c)
		xml2Guard(t, p, b, c, "public", "xml2_text")
		xml2RecreatedPrincipal(t, p, b, c)
	})
}
