package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictIntIdentity(t *testing.T) {
	dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE SCHEMA dict_int_addon", "CREATE SCHEMA dict_int_moved", "CREATE ROLE dict_int_owner", "CREATE EXTENSION dict_int SCHEMA dict_int_addon"} {
			dictIntExec(t, p, b, c, q, 0)
		}
		dictIntInstallation(t, p, b, c, "dict_int_addon")
		ext := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_extension WHERE extname='dict_int'")
		dictionary := dictIntID(t, p, c, "SELECT 'dict_int_addon.intdict'::regdictionary::oid::bigint")
		template := dictIntID(t, p, c, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid=$1", dictionary)
		initOID := dictIntID(t, p, c, "SELECT tmplinit::oid::bigint FROM pg_ts_template WHERE oid=$1", template)
		lexize := dictIntID(t, p, c, "SELECT tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1", template)
		held := dictIntHeld(t, p, c)
		defer held.Statement.Close()
		dictIntLexemes(t, p, b, held, "{-12345}", "dict_int_addon.intdict", "-123456")
		for _, q := range []string{
			"ALTER FUNCTION dict_int_addon.dintdict_init(internal) RENAME TO dict_int_init_moved",
			"ALTER FUNCTION dict_int_addon.dict_int_init_moved(internal) SET SCHEMA dict_int_moved",
			"ALTER FUNCTION dict_int_addon.dintdict_lexize(internal,internal,internal,internal) RENAME TO dict_int_lexize_moved",
			"ALTER FUNCTION dict_int_addon.dict_int_lexize_moved(internal,internal,internal,internal) SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH TEMPLATE dict_int_addon.intdict_template RENAME TO dict_int_template_moved",
			"ALTER TEXT SEARCH TEMPLATE dict_int_addon.dict_int_template_moved SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH DICTIONARY dict_int_addon.intdict RENAME TO dict_int_member_moved",
			"ALTER TEXT SEARCH DICTIONARY dict_int_addon.dict_int_member_moved SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved (maxlen=3,absval=true)",
			"ALTER TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved OWNER TO dict_int_owner",
			"ALTER FUNCTION dict_int_moved.dict_int_init_moved(internal) OWNER TO dict_int_owner",
			"ALTER FUNCTION dict_int_moved.dict_int_lexize_moved(internal,internal,internal,internal) OWNER TO dict_int_owner",
		} {
			dictIntExec(t, p, b, c, q, 0)
		}
		moved := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_namespace WHERE nspname='dict_int_moved'")
		dictIntSelect(t, p, b, c, "SELECT d.oid::bigint,d.dictname,d.dictnamespace::bigint,d.dicttemplate::bigint,d.dictinitoption,r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid=$1", []string{"oid", "dictname", "dictnamespace", "dicttemplate", "dictinitoption", "rolname"}, []string{"INT8", "NAME", "INT8", "INT8", "TEXT", "NAME"}, [][]any{{dictionary, "dict_int_member_moved", moved, template, "maxlen = 3, absval = 'true'", "dict_int_owner"}}, dictionary)
		dictIntSelect(t, p, b, c, "SELECT oid::bigint,tmplname,tmplnamespace::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1", []string{"oid", "tmplname", "tmplnamespace", "tmplinit", "tmpllexize"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{{template, "dict_int_template_moved", moved, initOID, lexize}}, template)
		dictIntSelect(t, p, b, c, "SELECT p.oid::bigint,p.proname,p.pronamespace::bigint,p.proisstrict,p.provolatile,p.proparallel,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1 OR p.oid=$2 ORDER BY p.proname", []string{"oid", "proname", "pronamespace", "proisstrict", "provolatile", "proparallel", "rolname"}, []string{"INT8", "NAME", "INT8", "BOOL", "CHAR", "CHAR", "NAME"}, [][]any{{initOID, "dict_int_init_moved", moved, true, "v", "u", "dict_int_owner"}, {lexize, "dict_int_lexize_moved", moved, true, "v", "u", "dict_int_owner"}}, initOID, lexize)
		dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, ext)
		for _, e := range []fixture.Executor{c, held} {
			dictIntLexemes(t, p, b, e, "{123}", "dict_int_moved.dict_int_member_moved", "-123456")
		}
		// An ordinary template gets the released body from the authentic
		// callback pair, despite its names, location and current membership.
		for _, q := range []string{
			"CREATE TEXT SEARCH TEMPLATE public.dict_int_ordinary_template (INIT=dict_int_moved.dict_int_init_moved,LEXIZE=dict_int_moved.dict_int_lexize_moved)",
			"CREATE TEXT SEARCH DICTIONARY public.dict_int_ordinary (TEMPLATE=public.dict_int_ordinary_template,maxlen=2,absval=true)",
			"CREATE TEXT SEARCH TEMPLATE dict_int_addon.intdict_template (INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)",
			"CREATE TEXT SEARCH DICTIONARY dict_int_addon.intdict (TEMPLATE=dict_int_addon.intdict_template)",
			"CREATE EXTENSION unaccent",
		} {
			dictIntExec(t, p, b, c, q, 0)
		}
		dictIntLexemes(t, p, b, held, "{12}", "public.dict_int_ordinary", "-123456")
		dictIntLexemes(t, p, b, held, "{abcdefghi}", "dict_int_addon.intdict", "ABCdefghi")
		for _, member := range []string{"FUNCTION dict_int_moved.dict_int_init_moved(internal)", "FUNCTION dict_int_moved.dict_int_lexize_moved(internal,internal,internal,internal)", "TEXT SEARCH TEMPLATE dict_int_moved.dict_int_template_moved", "TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved"} {
			dictIntExec(t, p, b, c, "ALTER EXTENSION dict_int DROP "+member, 0)
			dictIntLexemes(t, p, b, held, "{123}", "dict_int_moved.dict_int_member_moved", "-123456")
			dictIntExec(t, p, b, c, "ALTER EXTENSION unaccent ADD "+member, 0)
			dictIntLexemes(t, p, b, held, "{123}", "dict_int_moved.dict_int_member_moved", "-123456")
		}
		dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, ext)
		dictIntLexemes(t, p, b, held, "{12}", "public.dict_int_ordinary", "-123456")
		for _, member := range []string{"FUNCTION dict_int_moved.dict_int_init_moved(internal)", "FUNCTION dict_int_moved.dict_int_lexize_moved(internal,internal,internal,internal)", "TEXT SEARCH TEMPLATE dict_int_moved.dict_int_template_moved", "TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved"} {
			dictIntExec(t, p, b, c, "ALTER EXTENSION unaccent DROP "+member, 0)
			dictIntExec(t, p, b, c, "ALTER EXTENSION dict_int ADD "+member, 0)
		}
	})
}
