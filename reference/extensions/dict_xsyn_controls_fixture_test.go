package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type xsynControlNames struct {
	member, template, init, lexize, helperOwner string
	options, lexemes                            any
	members                                     int64
}

func xsynControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mutated bool) xsynControlNames {
	t.Helper()
	var installer string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&installer); err != nil {
		t.Fatal(err)
	}
	n := xsynControlNames{"public.xsyn", "public.xsyn_template", "public.dxsyn_init", "public.dxsyn_lexize", installer, nil, nil, 4}
	for _, q := range []string{"CREATE ROLE xsyn_owner", "CREATE ROLE xsyn_denied", "GRANT USAGE ON SCHEMA public TO xsyn_denied", "CREATE EXTENSION dict_xsyn", "COMMENT ON EXTENSION dict_xsyn IS 'xsyn controls'"} {
		xsynExec(t, p, b, c, q, 0)
	}
	if mutated {
		for _, q := range []string{
			"CREATE SCHEMA xsyn_moved",
			"ALTER FUNCTION public.dxsyn_init(internal) RENAME TO xsyn_init_moved",
			"ALTER FUNCTION public.xsyn_init_moved(internal) SET SCHEMA xsyn_moved",
			"ALTER FUNCTION public.dxsyn_lexize(internal,internal,internal,internal) RENAME TO xsyn_lexize_moved",
			"ALTER FUNCTION public.xsyn_lexize_moved(internal,internal,internal,internal) SET SCHEMA xsyn_moved",
			"ALTER TEXT SEARCH TEMPLATE public.xsyn_template RENAME TO xsyn_template_moved",
			"ALTER TEXT SEARCH TEMPLATE public.xsyn_template_moved SET SCHEMA xsyn_moved",
			"ALTER TEXT SEARCH DICTIONARY public.xsyn RENAME TO xsyn_member_moved",
			"ALTER TEXT SEARCH DICTIONARY public.xsyn_member_moved SET SCHEMA xsyn_moved",
			"ALTER TEXT SEARCH DICTIONARY xsyn_moved.xsyn_member_moved (rules=xsyn_sample,matchsynonyms=true,keeporig=false)",
			"ALTER TEXT SEARCH DICTIONARY xsyn_moved.xsyn_member_moved OWNER TO xsyn_owner",
			"ALTER FUNCTION xsyn_moved.xsyn_init_moved(internal) OWNER TO xsyn_owner",
			"ALTER FUNCTION xsyn_moved.xsyn_lexize_moved(internal,internal,internal,internal) OWNER TO xsyn_owner",
			"ALTER EXTENSION dict_xsyn DROP TEXT SEARCH DICTIONARY xsyn_moved.xsyn_member_moved",
		} {
			xsynExec(t, p, b, c, q, 0)
		}
		n = xsynControlNames{"xsyn_moved.xsyn_member_moved", "xsyn_moved.xsyn_template_moved", "xsyn_moved.xsyn_init_moved", "xsyn_moved.xsyn_lexize_moved", "xsyn_owner", "rules = 'xsyn_sample', matchsynonyms = 'true', keeporig = 'false'", xsynSynonyms, 3}
	}
	for _, q := range []string{
		"COMMENT ON TEXT SEARCH DICTIONARY " + n.member + " IS 'retained xsyn member'",
		"CREATE TEXT SEARCH DICTIONARY public.xsyn_custom (TEMPLATE=" + n.template + ",rules=xsyn_sample,matchsynonyms=true)",
		"ALTER TEXT SEARCH DICTIONARY public.xsyn_custom OWNER TO xsyn_owner",
		"COMMENT ON TEXT SEARCH DICTIONARY public.xsyn_custom IS 'custom group'",
		"REVOKE ALL ON FUNCTION " + n.lexize + "(internal,internal,internal,internal) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION " + n.lexize + "(internal,internal,internal,internal) TO xsyn_owner",
		"CREATE TEXT SEARCH CONFIGURATION public.xsyn_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION public.xsyn_cfg ALTER MAPPING FOR asciiword WITH public.xsyn_custom,pg_catalog.simple",
		"ALTER TEXT SEARCH CONFIGURATION public.xsyn_cfg OWNER TO xsyn_owner",
		"CREATE SCHEMA xsyn_addon AUTHORIZATION xsyn_owner",
		"REVOKE ALL ON SCHEMA xsyn_addon FROM PUBLIC",
		"CREATE TEXT SEARCH CONFIGURATION xsyn_addon.xsyn_guard (COPY=public.xsyn_cfg)",
		"ALTER TEXT SEARCH CONFIGURATION xsyn_addon.xsyn_guard OWNER TO xsyn_owner",
		"CREATE TABLE public.xsyn_vectors(id int,v tsvector,d regdictionary DEFAULT 'public.xsyn_custom'::regdictionary,m regdictionary DEFAULT '" + n.member + "'::regdictionary)",
		"CREATE VIEW public.xsyn_saved AS SELECT 'public.xsyn_custom'::regdictionary AS d,'" + n.member + "'::regdictionary AS m",
	} {
		xsynExec(t, p, b, c, q, 0)
	}
	xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors(id,v) VALUES(1,to_tsvector('xsyn_cfg','supernova')),(2,to_tsvector('xsyn_cfg','grb')),(3,NULL)", 3)
	return n
}

func xsynControlState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n xsynControlNames, mutated bool) {
	t.Helper()
	xsynLexemes(t, p, b, c, n.lexemes, n.member, "supernova")
	xsynOptions(t, p, b, c, n.member, n.options)
	h := xsynHeld(t, p, c)
	defer h.Statement.Close()
	for _, e := range []fixture.Executor{c, h} {
		xsynLexemes(t, p, b, e, xsynAll, "xsyn_custom", "sn")
	}
	xsynOptions(t, p, b, c, "xsyn_custom", "rules = 'xsyn_sample', matchsynonyms = 'true'")
	templateNames := strings.Split(n.template, ".")
	xsynSelect(t, p, b, c, "SELECT t.tmplname,ns.nspname FROM pg_ts_template t JOIN pg_namespace ns ON ns.oid=t.tmplnamespace JOIN pg_ts_dict d ON d.dicttemplate=t.oid WHERE d.oid=$1::regdictionary::oid", []string{"tmplname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{templateNames[1], templateNames[0]}}, n.member)
	xsynSelect(t, p, b, c, "SELECT d.dicttemplate=t.oid AS linked,t.tmplinit=$2::regprocedure AS init,t.tmpllexize=$3::regprocedure AS lexize FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.oid=$1::regdictionary::oid", []string{"linked", "init", "lexize"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, n.member, n.init+"(internal)", n.lexize+"(internal,internal,internal,internal)")
	xsynSelect(t, p, b, c, "SELECT d.dicttemplate=m.dicttemplate AS linked,r.rolname,obj_description(d.oid,'pg_ts_dict') AS comment FROM pg_ts_dict d JOIN pg_ts_dict m ON m.oid=$1::regdictionary::oid JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid='xsyn_custom'::regdictionary::oid", []string{"linked", "rolname", "comment"}, []string{"BOOL", "NAME", "TEXT"}, [][]any{{true, "xsyn_owner", "custom group"}}, n.member)
	xsynSelect(t, p, b, c, "SELECT obj_description(oid,'pg_ts_dict') AS comment FROM pg_ts_dict WHERE oid=$1::regdictionary::oid", []string{"comment"}, []string{"TEXT"}, [][]any{{"retained xsyn member"}}, n.member)
	xsynSelect(t, p, b, c, "SELECT e.extversion,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='dict_xsyn'", []string{"extversion", "nspname", "comment"}, []string{"TEXT", "NAME", "TEXT"}, [][]any{{"1.0", "public", "xsyn controls"}})
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.refclassid=3079 AND e.extname='dict_xsyn' AND dep.deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{n.members}})
	memberEdges := int64(1)
	if mutated {
		memberEdges = 0
		xsynSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid=$1::regdictionary::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{"xsyn_owner"}}, n.member)
	}
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1::regdictionary::oid AND refclassid=3079 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{memberEdges}}, n.member)
	for _, callback := range []struct{ name, args, signature, source string }{
		{n.init, "2281", "(internal)", "dxsyn_init"}, {n.lexize, "2281 2281 2281 2281", "(internal,internal,internal,internal)", "dxsyn_lexize"},
	} {
		names := strings.Split(callback.name, ".")
		xsynSelect(t, p, b, c, "SELECT p.proname,ns.nspname,r.rolname,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proparallel,p.probin,p.prosrc FROM pg_proc p JOIN pg_namespace ns ON ns.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure::oid", []string{"proname", "nspname", "rolname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "probin", "prosrc"}, []string{"NAME", "NAME", "NAME", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT"}, [][]any{{names[1], names[0], n.helperOwner, callback.args, int64(2281), true, "v", "u", "$libdir/dict_xsyn", callback.source}}, callback.name+callback.signature)
	}
	xsynSelect(t, p, b, c, "SELECT has_function_privilege('xsyn_owner',$1,'EXECUTE') AS owner,has_function_privilege('xsyn_denied',$1,'EXECUTE') AS denied", []string{"owner", "denied"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, n.lexize+"(internal,internal,internal,internal)")
	for _, dictionary := range []string{n.member, "public.xsyn_custom"} {
		xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_dict d ON d.oid=dep.objid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3600 AND dep.refclassid=3764 AND dep.refobjid=d.dicttemplate AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, dictionary)
	}
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_template t ON t.oid=dep.objid JOIN pg_ts_dict d ON d.dicttemplate=t.oid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3764 AND dep.refclassid=1255 AND (dep.refobjid=t.tmplinit::oid OR dep.refobjid=t.tmpllexize::oid) AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, n.member)
	xsynSelect(t, p, b, c, "SELECT mapseqno,mapdict='xsyn_custom'::regdictionary::oid AS dictionary FROM pg_ts_config_map WHERE mapcfg='xsyn_cfg'::regconfig::oid AND maptokentype=1 ORDER BY mapseqno", []string{"mapseqno", "dictionary"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}})
	xsynSelect(t, p, b, c, "SELECT cfg.cfgparser::bigint,r.rolname FROM pg_ts_config cfg JOIN pg_roles r ON r.oid=cfg.cfgowner WHERE cfg.oid='xsyn_cfg'::regconfig::oid", []string{"cfgparser", "rolname"}, []string{"INT8", "NAME"}, [][]any{{int64(3722), "xsyn_owner"}})
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3602 AND objid='xsyn_cfg'::regconfig::oid AND refclassid=3600 AND refobjid='xsyn_custom'::regdictionary::oid AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
	xsynSelect(t, p, b, c, "SELECT id,v::text,d='xsyn_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM xsyn_vectors ORDER BY id", []string{"id", "v", "dictionary", "member"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "'1987a':1 'sn':1 'sne':1 'supernova':1", true, true}, {int64(2), "'grb':1", true, true}, {int64(3), nil, true, true}}, n.member)
	xsynSelect(t, p, b, c, "SELECT d='xsyn_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM xsyn_saved", []string{"dictionary", "member"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, n.member)
	xsynSelect(t, p, b, c, "SELECT to_tsvector('xsyn_addon.xsyn_guard','sn')::text AS vector", []string{"vector"}, []string{"TEXT"}, [][]any{{"'1987a':1 'sn':1 'sne':1 'supernova':1"}})
	xsynExec(t, p, b, c, "INSERT INTO xsyn_vectors(id) VALUES(9)", 1)
	xsynSelect(t, p, b, c, "SELECT d='xsyn_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM xsyn_vectors WHERE id=9", []string{"dictionary", "member"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, n.member)
	xsynExec(t, p, b, c, "DELETE FROM xsyn_vectors WHERE id=9", 1)
}

func xsynControlCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n xsynControlNames, mutated bool) {
	t.Helper()
	for _, q := range []string{"DROP VIEW xsyn_saved", "DROP TABLE xsyn_vectors", "DROP TEXT SEARCH CONFIGURATION xsyn_addon.xsyn_guard", "DROP TEXT SEARCH CONFIGURATION xsyn_cfg", "DROP TEXT SEARCH DICTIONARY xsyn_custom"} {
		xsynExec(t, p, b, c, q, 0)
	}
	if mutated {
		xsynExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY "+n.member, 0)
	}
	xsynExec(t, p, b, c, "DROP EXTENSION dict_xsyn CASCADE", 0)
	if mutated {
		xsynExec(t, p, b, c, "DROP SCHEMA xsyn_moved", 0)
	}
	xsynExec(t, p, b, c, "DROP SCHEMA xsyn_addon", 0)
}

// Match an actual class-3600 TEMP object to the imported dictionary's source OID.
func xsynCollision(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source int64) func() {
	t.Helper()
	for i := int64(0); i <= source; i++ {
		name := fmt.Sprintf("xsyn_collision_%d", i)
		qualified := "pg_temp." + name
		xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+qualified+" (TEMPLATE=pg_catalog.simple,accept=false)", 0)
		oid := dictIntID(t, p, c, "SELECT $1::regdictionary::oid::bigint", qualified)
		if oid > source {
			t.Fatalf("TEMP collision overshot source: %d > %d", oid, source)
		}
		if oid != source {
			continue
		}
		const state = "SELECT oid::bigint,dictname,dictnamespace::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1"
		xsynOptions(t, p, b, c, qualified, "accept = 'false'")
		before := dictIntRows(t, p, c, state, source)
		check := func() {
			dictIntUnchanged(t, p, c, before, state, source)
			xsynSelect(t, p, b, c, "SELECT $1::regdictionary::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{source}}, qualified)
			xsynLexemes(t, p, b, c, nil, qualified, "WORD")
		}
		check()
		return check
	}
	t.Fatal("exact TEMP dictionary collision not reached")
	return nil
}
