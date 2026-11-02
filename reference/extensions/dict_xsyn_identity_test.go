package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictXsynIdentity(t *testing.T) {
	xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE SCHEMA xsyn_addon", "CREATE SCHEMA xsyn_moved", "CREATE ROLE xsyn_owner", "CREATE EXTENSION dict_xsyn SCHEMA xsyn_addon", "ALTER TEXT SEARCH DICTIONARY xsyn_addon.xsyn (rules=xsyn_sample,matchsynonyms=true)"} {
			xsynExec(t, p, b, c, q, 0)
		}
		dictionary := dictIntID(t, p, c, "SELECT 'xsyn_addon.xsyn'::regdictionary::oid::bigint")
		template := dictIntID(t, p, c, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid=$1", dictionary)
		init := dictIntID(t, p, c, "SELECT tmplinit::oid::bigint FROM pg_ts_template WHERE oid=$1", template)
		lexize := dictIntID(t, p, c, "SELECT tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1", template)
		h := xsynHeld(t, p, c)
		defer h.Statement.Close()
		xsynLexemes(t, p, b, h, xsynAll, "xsyn_addon.xsyn", "sn")
		xsynExec(t, p, b, c, "ALTER EXTENSION dict_xsyn SET SCHEMA xsyn_moved", 0)
		xsynSelect(t, p, b, c, "SELECT d.oid=$1 AS dictionary,d.dicttemplate=$2 AS template,t.tmplinit::oid=$3 AS init,t.tmpllexize::oid=$4 AS lexize FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.oid='xsyn_moved.xsyn'::regdictionary::oid", []string{"dictionary", "template", "init", "lexize"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, dictionary, template, init, lexize)
		for _, q := range []string{
			"ALTER FUNCTION xsyn_moved.dxsyn_init(internal) RENAME TO xsyn_init_moved",
			"ALTER FUNCTION xsyn_moved.xsyn_init_moved(internal) SET SCHEMA xsyn_addon",
			"ALTER FUNCTION xsyn_moved.dxsyn_lexize(internal,internal,internal,internal) RENAME TO xsyn_lexize_moved",
			"ALTER FUNCTION xsyn_moved.xsyn_lexize_moved(internal,internal,internal,internal) SET SCHEMA xsyn_addon",
			"ALTER TEXT SEARCH TEMPLATE xsyn_moved.xsyn_template RENAME TO xsyn_template_moved",
			"ALTER TEXT SEARCH TEMPLATE xsyn_moved.xsyn_template_moved SET SCHEMA xsyn_addon",
			"ALTER TEXT SEARCH DICTIONARY xsyn_moved.xsyn RENAME TO xsyn_member_moved",
			"ALTER TEXT SEARCH DICTIONARY xsyn_moved.xsyn_member_moved SET SCHEMA xsyn_addon",
			"ALTER TEXT SEARCH DICTIONARY xsyn_addon.xsyn_member_moved OWNER TO xsyn_owner",
			"ALTER FUNCTION xsyn_addon.xsyn_init_moved(internal) OWNER TO xsyn_owner",
			"ALTER FUNCTION xsyn_addon.xsyn_lexize_moved(internal,internal,internal,internal) OWNER TO xsyn_owner",
			"CREATE TEXT SEARCH TEMPLATE public.xsyn_ordinary_template (INIT=xsyn_addon.xsyn_init_moved,LEXIZE=xsyn_addon.xsyn_lexize_moved)",
			"CREATE TEXT SEARCH DICTIONARY public.xsyn_ordinary (TEMPLATE=public.xsyn_ordinary_template,rules=xsyn_sample,keeporig=false)",
			"CREATE TEXT SEARCH TEMPLATE xsyn_moved.xsyn_template (INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)",
			"CREATE TEXT SEARCH DICTIONARY xsyn_moved.xsyn (TEMPLATE=xsyn_moved.xsyn_template)",
		} {
			xsynExec(t, p, b, c, q, 0)
		}
		check := func() {
			for _, e := range []fixture.Executor{c, h} {
				xsynLexemes(t, p, b, e, xsynAll, "xsyn_addon.xsyn_member_moved", "SN")
				xsynLexemes(t, p, b, e, xsynSynonyms, "xsyn_ordinary", "supernova")
				xsynLexemes(t, p, b, e, "{supernova}", "xsyn_moved.xsyn", "SUPERNOVA")
			}
			xsynSelect(t, p, b, c, "SELECT d.oid::bigint,d.dicttemplate::bigint,t.tmplinit::oid::bigint,t.tmpllexize::oid::bigint,r.rolname FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid='xsyn_addon.xsyn_member_moved'::regdictionary::oid", []string{"oid", "dicttemplate", "tmplinit", "tmpllexize", "rolname"}, []string{"INT8", "INT8", "INT8", "INT8", "NAME"}, [][]any{{dictionary, template, init, lexize, "xsyn_owner"}})
		}
		check()
		// Current membership may change without replacing the sealed helpers.
		xsynExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		for _, member := range []string{"FUNCTION xsyn_addon.xsyn_init_moved(internal)", "FUNCTION xsyn_addon.xsyn_lexize_moved(internal,internal,internal,internal)", "TEXT SEARCH TEMPLATE xsyn_addon.xsyn_template_moved", "TEXT SEARCH DICTIONARY xsyn_addon.xsyn_member_moved"} {
			xsynExec(t, p, b, c, "ALTER EXTENSION dict_xsyn DROP "+member, 0)
			check()
			xsynExec(t, p, b, c, "ALTER EXTENSION unaccent ADD "+member, 0)
			check()
		}
		for _, member := range []string{"FUNCTION xsyn_addon.xsyn_init_moved(internal)", "FUNCTION xsyn_addon.xsyn_lexize_moved(internal,internal,internal,internal)", "TEXT SEARCH TEMPLATE xsyn_addon.xsyn_template_moved", "TEXT SEARCH DICTIONARY xsyn_addon.xsyn_member_moved"} {
			xsynExec(t, p, b, c, "ALTER EXTENSION unaccent DROP "+member, 0)
			xsynExec(t, p, b, c, "ALTER EXTENSION dict_xsyn ADD "+member, 0)
		}
		check()
	})
}
