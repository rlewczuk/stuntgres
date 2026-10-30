package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// These are actual object DROP paths, not dependency-label stand-ins. Their
// shared closure must reject direct extension members even under CASCADE.
func ownershipClassMemberDrop(tc ownershipClassCase, schema string) (fixture.Step, bool) {
	var kind string
	switch tc.name {
	case "table":
		kind = "table"
	case "foreign_table":
		kind = "foreign table"
	case "view":
		kind = "view"
	case "materialized_view":
		kind = "materialized view"
	case "sequence":
		kind = "sequence"
	case "parser":
		kind = "text search parser"
	case "dictionary_snowball":
		kind = "text search dictionary"
	default:
		return fixture.Step{}, false
	}
	step := fixture.OwnershipError("DROP "+kind+" "+schema+".item CASCADE", "2BP01", "cannot drop "+kind+" "+schema+".item because extension unaccent requires it")
	step.Diagnostic.Hint = "You can drop extension unaccent instead."
	return step, true
}

func ownershipClassWitness(run func(fixture.Step), name, schema string) {
	text := func(q, value string) { run(fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{value}})) }
	integer := func(q string, value int64) {
		run(fixture.Select(q, []string{"value"}, []string{"INT4"}, [][]any{{value}}))
	}
	switch name {
	case "function", "routine":
		integer("SELECT "+schema+".item(4) AS value", 4)
	case "procedure":
		run(fixture.Exec("CALL "+schema+".item(4)", 0))
	case "aggregate":
		integer("SELECT "+schema+".item(v) AS value FROM (VALUES(1),(2)) AS input(v)", 3)
	case "enum":
		text("SELECT 'one'::"+schema+".item::text AS value", "one")
	case "domain":
		text("SELECT 4::"+schema+".item::text AS value", "4")
	case "range":
		text("SELECT '[1,3)'::"+schema+".item::text AS value", "[1,3)")
	case "table":
		text("SELECT ROW(4,'owner')::"+schema+".item::text AS value", "(4,owner)")
	case "foreign_table":
		text("SELECT ROW(4)::"+schema+".item::text AS value", "(4)")
	case "view":
		integer("SELECT v AS value FROM "+schema+".item", 1)
	case "materialized_view":
		run(fixture.Exec("REFRESH MATERIALIZED VIEW "+schema+".item", 0))
		integer("SELECT v AS value FROM "+schema+".item", 1)
	case "sequence":
		run(fixture.Select("SELECT setval('"+schema+".item',40,false) AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(40)}}))
		run(fixture.Select("SELECT nextval('"+schema+".item') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(40)}}))
	case "operator":
		integer("SELECT 4 OPERATOR("+schema+".#~#) 5 AS value", 9)
	case "operator_family":
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_opfamily f JOIN pg_am a ON a.oid=f.opfmethod WHERE f.opfname='item' AND f.opfnamespace='"+schema+"'::regnamespace AND a.amname='btree'", 1))
	case "operator_class":
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_opclass c JOIN pg_am a ON a.oid=c.opcmethod WHERE c.opcname='item' AND c.opcnamespace='"+schema+"'::regnamespace AND a.amname='btree' AND c.opcintype='integer'::regtype", 1))
	case "dictionary":
		run(fixture.Select("SELECT ts_lexize('"+schema+".item','HELLO') AS value", []string{"value"}, []string{"_TEXT"}, [][]any{{"{hello}"}}))
		ownershipDictionaryTemplateDependency(run, schema+".item")
	case "dictionary_snowball":
		ownershipSnowballDictionaryWitness(run, schema+".item", true)
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_ts_dict WHERE oid='"+schema+".item'::regdictionary AND dictowner=current_user::regrole AND dicttemplate=(SELECT oid FROM pg_ts_template WHERE tmplnamespace='pg_catalog'::regnamespace AND tmplname='snowball') AND dictinitoption=$1", 1, "language = 'english', stopwords = 'english'"))
		ownershipDictionaryTemplateDependency(run, schema+".item")
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_depend WHERE classid='pg_ts_dict'::regclass AND objid='"+schema+".item'::regdictionary AND refclassid='pg_namespace'::regclass AND refobjid='"+schema+"'::regnamespace AND deptype='n'", 1))
	case "parser":
		ownershipParserWitness(run, schema)
	case "configuration":
		s := fixture.Select("SELECT to_tsvector('"+schema+".item'::regconfig,$1::text) AS value", []string{"value"}, []string{"TSVECTOR"}, [][]any{{"'fat':2 'rats':3 'the':1"}}, "The Fat Rats")
		run(s)
		run(fixture.Prepared(s))
		s = fixture.Select("SELECT to_tsvector('"+schema+".item'::regconfig,NULL::text) IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{true}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_ts_config c JOIN pg_ts_parser p ON p.oid=c.cfgparser WHERE c.oid='"+schema+".item'::regconfig AND p.prsname='default' AND p.prsnamespace='pg_catalog'::regnamespace", 1))
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_ts_config_map m WHERE m.mapcfg='"+schema+".item'::regconfig AND m.mapdict='pg_catalog.simple'::regdictionary", 19))
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_depend WHERE classid='pg_ts_config'::regclass AND objid='"+schema+".item'::regconfig AND refclassid='pg_ts_config'::regclass", 0))
	case "collation":
		text("SELECT ('a' COLLATE "+schema+".item)::text AS value", "a")
	case "conversion":
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_conversion WHERE connamespace='"+schema+"'::regnamespace AND conname='item' AND conforencoding=6 AND contoencoding=8 AND conproc='pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)'::regprocedure", 1))
	case "schema":
		run(fixture.Exec(`CREATE TABLE eo_other.eo_witness(v integer)`, 0))
		run(fixture.Exec(`DROP TABLE eo_other.eo_witness`, 0))
	case "language":
		run(fixture.Select(`CALL public.eo_witness(4)`, []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}}))
	case "access_method":
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_am WHERE amname='eo_method' AND amtype='i' AND amhandler='pg_catalog.bthandler(internal)'::regprocedure`, 1))
	case "event_trigger":
		run(fixture.Exec(`ALTER EVENT TRIGGER eo_event ENABLE`, 0))
		run(fixture.Exec(`CREATE TABLE public.eo_witness(v integer)`, 0))
		run(fixture.Exec(`DROP TABLE public.eo_witness`, 0))
		run(fixture.Exec(`ALTER EVENT TRIGGER eo_event DISABLE`, 0))
	case "foreign_wrapper":
		run(fixture.Exec(`CREATE SERVER eo_witness FOREIGN DATA WRAPPER eo_fdw`, 0))
		run(fixture.Exec(`DROP SERVER eo_witness`, 0))
	case "server":
		run(fixture.Exec("CREATE FOREIGN TABLE "+schema+".eo_witness(v integer) SERVER eo_server", 0))
		run(fixture.Exec("DROP FOREIGN TABLE "+schema+".eo_witness", 0))
	case "cast":
		text("SELECT 'one'::"+schema+".source::"+schema+".destination::text AS value", "one")
	}
}

func ownershipDictionaryTemplateDependency(run func(fixture.Step), dictionary string) {
	// Fixed simple/synonym templates are pinned; allocated initdb templates
	// such as Snowball are not. Derive this expectation from the actual current
	// template identity, not the dictionary's fixture name or backend.
	run(fixture.Select("SELECT (SELECT count(*) FROM pg_depend dep WHERE dep.classid='pg_ts_dict'::regclass AND dep.objid=d.oid AND dep.refclassid='pg_ts_template'::regclass AND dep.refobjid=d.dicttemplate AND dep.deptype='n') = CASE WHEN d.dicttemplate IN (3727,3730) THEN 0 ELSE 1 END AS valid FROM pg_ts_dict d WHERE d.oid='"+dictionary+"'::regdictionary", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}))
}

// Symbolic dependency endpoints and named principals are comparable across real
// independent remaps; numerical OIDs are deliberately not compared here.
func ownershipClassMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn, tc ownershipClassCase, id int64) [][][]any {
	t.Helper()
	q := `SELECT pg_describe_object(refclassid,refobjid,refobjsubid) AS endpoint,deptype FROM pg_depend WHERE classid=` + fixture.OwnershipNumber(tc.class) + ` AND objid=` + fixture.OwnershipNumber(id) + ` AND deptype<>'e' ORDER BY endpoint,deptype`
	out := [][][]any{fixture.OwnershipSnapshot(t, p, c, q)}
	owners := map[int64][3]string{
		1255: {"pg_proc", "proowner", "proacl"}, 1247: {"pg_type", "typowner", "typacl"}, 1259: {"pg_class", "relowner", "relacl"}, 2617: {"pg_operator", "oprowner", "NULL"}, 2753: {"pg_opfamily", "opfowner", "NULL"}, 2616: {"pg_opclass", "opcowner", "NULL"}, 3600: {"pg_ts_dict", "dictowner", "NULL"}, 3456: {"pg_collation", "collowner", "NULL"}, 2607: {"pg_conversion", "conowner", "NULL"}, 2615: {"pg_namespace", "nspowner", "nspacl"}, 2612: {"pg_language", "lanowner", "lanacl"}, 3466: {"pg_event_trigger", "evtowner", "NULL"}, 2328: {"pg_foreign_data_wrapper", "fdwowner", "fdwacl"}, 1417: {"pg_foreign_server", "srvowner", "srvacl"},
		3602: {"pg_ts_config", "cfgowner", "NULL"},
	}
	if owner, ok := owners[tc.class]; ok {
		out = append(out, fixture.OwnershipSnapshot(t, p, c, "SELECT r.rolname,"+owner[2]+"::text FROM "+owner[0]+" o JOIN pg_roles r ON r.oid=o."+owner[1]+" WHERE o.oid="+fixture.OwnershipNumber(id)))
	}
	if tc.class == 3600 {
		out = append(out, fixture.OwnershipSnapshot(t, p, c, `SELECT dictinitoption,pg_describe_object('pg_ts_template'::regclass,dicttemplate,0) FROM pg_ts_dict WHERE oid=`+fixture.OwnershipNumber(id)))
	}
	if tc.class == 3602 {
		out = append(out, fixture.OwnershipSnapshot(t, p, c, `SELECT m.maptokentype,m.mapseqno,pg_describe_object('pg_ts_dict'::regclass,m.mapdict,0) AS dictionary FROM pg_ts_config_map m WHERE m.mapcfg=`+fixture.OwnershipNumber(id)+` ORDER BY m.maptokentype,m.mapseqno`))
	}
	if tc.class == 3601 {
		out = append(out, fixture.OwnershipSnapshot(t, p, c, `SELECT n.nspname,p.prsname,p.prsstart::oid::bigint,p.prstoken::oid::bigint,p.prsend::oid::bigint,p.prsheadline::oid::bigint,p.prslextype::oid::bigint FROM pg_ts_parser p JOIN pg_namespace n ON n.oid=p.prsnamespace WHERE p.oid=`+fixture.OwnershipNumber(id)))
		config := fixture.OwnershipID(t, p, c, `SELECT 'eo_old.parser_config'::regconfig::bigint`)
		out = append(out, fixture.OwnershipSnapshot(t, p, c, `SELECT n.nspname,c.cfgname,r.rolname,pg_describe_object('pg_ts_parser'::regclass,c.cfgparser,0) FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace JOIN pg_roles r ON r.oid=c.cfgowner WHERE c.oid=`+fixture.OwnershipNumber(config)))
		out = append(out, fixture.OwnershipSnapshot(t, p, c, `SELECT m.maptokentype,m.mapseqno,pg_describe_object('pg_ts_dict'::regclass,m.mapdict,0) FROM pg_ts_config_map m WHERE m.mapcfg=`+fixture.OwnershipNumber(config)+` ORDER BY m.maptokentype,m.mapseqno`))
	}
	return out
}

func ownershipClassMetadataEqual(t *testing.T, want, got [][][]any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("class replay changed symbolic dependencies/owner/ACL: before=%v after=%v", want, got)
	}
}

// Same-source comparisons retain real parser, owner and ordered dictionary OIDs;
// the cross-driver metadata comparator above uses symbolic dependency endpoints.
func ownershipConfigurationSnapshot(t *testing.T, p *fixture.Pair, c *sql.Conn, id int64) [][][]any {
	return [][][]any{
		fixture.OwnershipSnapshot(t, p, c, `SELECT cfgowner::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=`+fixture.OwnershipNumber(id)),
		fixture.OwnershipSnapshot(t, p, c, `SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg=`+fixture.OwnershipNumber(id)+` ORDER BY maptokentype,mapseqno`),
	}
}
