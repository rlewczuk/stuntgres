package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Reuse the ordinary parser DDL corpus's genuine supported callbacks. Membership
// never replaces this declaration with an installed-provider implementation.
func ownershipParserCreate(name string) string {
	return "CREATE TEXT SEARCH PARSER " + name + " (START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end,LEXTYPES=pg_catalog.prsd_lextype)"
}

func ownershipParserSetup() []string {
	return []string{ownershipParserCreate("eo_old.item"), `CREATE TEXT SEARCH CONFIGURATION eo_old.parser_config (PARSER=eo_old.item)`, `ALTER TEXT SEARCH CONFIGURATION eo_old.parser_config ADD MAPPING FOR asciiword WITH pg_catalog.simple`}
}

func ownershipParserWitness(run func(fixture.Step), schema string) {
	parser := `(SELECT oid FROM pg_ts_parser WHERE prsname='item' AND prsnamespace='` + schema + `'::regnamespace)`
	for _, input := range []any{"Word", "", nil} {
		var rows [][]any
		if input == "Word" {
			rows = [][]any{{int64(1), "Word"}}
		}
		s := fixture.Select("SELECT * FROM ts_parse('"+schema+".item',$1::text)", []string{"tokid", "token"}, []string{"INT4", "TEXT"}, rows, input)
		run(s)
		run(fixture.Prepared(s))
	}
	for _, s := range []fixture.Step{
		fixture.Select(`SELECT to_tsvector('eo_old.parser_config'::regconfig,$1::text) AS value`, []string{"value"}, []string{"TSVECTOR"}, [][]any{{"'word':1"}}, "Word"),
		fixture.Select(`SELECT to_tsvector('eo_old.parser_config'::regconfig,NULL::text) IS NULL AS absent`, []string{"absent"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select(`SELECT prsstart::oid::bigint,prstoken::oid::bigint,prsend::oid::bigint,prsheadline::oid::bigint,prslextype::oid::bigint FROM pg_ts_parser WHERE oid=`+parser, []string{"prsstart", "prstoken", "prsend", "prsheadline", "prslextype"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3717), int64(3718), int64(3719), int64(0), int64(3721)}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_config WHERE oid='eo_old.parser_config'::regconfig AND cfgparser=`+parser, 1))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_config_map WHERE mapcfg='eo_old.parser_config'::regconfig AND maptokentype=1 AND mapseqno=1 AND mapdict='pg_catalog.simple'::regdictionary`, 1))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_config_map WHERE mapcfg='eo_old.parser_config'::regconfig`, 1))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_config'::regclass AND objid='eo_old.parser_config'::regconfig AND refclassid='pg_ts_parser'::regclass AND refobjid=`+parser+` AND deptype='n'`, 1))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=`+parser+` AND refclassid='pg_namespace'::regclass AND refobjid='`+schema+`'::regnamespace AND deptype='n'`, 1))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=`+parser+` AND deptype='n'`, 1))
	// Referenced pinned functions suppress physical n rows, not real identities.
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=`+parser+` AND refclassid='pg_proc'::regclass AND deptype='n'`, 0))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND deptype='e' AND ((classid='pg_ts_config'::regclass AND objid='eo_old.parser_config'::regconfig) OR (classid='pg_proc'::regclass AND objid IN (3717,3718,3719,3721)))`, 0))
}

// Same-source comparisons preserve actual parser/configuration/callback identity;
// namespace is checked separately because relocation is intentional.
func ownershipParserSnapshot(t *testing.T, p *fixture.Pair, c *sql.Conn, id int64) [][][]any {
	return [][][]any{
		fixture.OwnershipSnapshot(t, p, c, `SELECT oid::bigint,prsname,prsstart::oid::bigint,prstoken::oid::bigint,prsend::oid::bigint,prsheadline::oid::bigint,prslextype::oid::bigint FROM pg_ts_parser WHERE oid=`+fixture.OwnershipNumber(id)),
		fixture.OwnershipSnapshot(t, p, c, `SELECT oid::bigint,cfgparser::bigint,cfgnamespace::bigint FROM pg_ts_config WHERE oid='eo_old.parser_config'::regconfig`),
		fixture.OwnershipSnapshot(t, p, c, `SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='eo_old.parser_config'::regconfig ORDER BY maptokentype,mapseqno`),
	}
}

func ownershipParserRestrict(schema string) fixture.Step {
	s := fixture.OwnershipError("DROP TEXT SEARCH PARSER "+schema+".item RESTRICT", "2BP01", "cannot drop text search parser "+schema+".item because other objects depend on it")
	s.Diagnostic.Detail = "text search configuration eo_old.parser_config depends on text search parser " + schema + ".item"
	s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return s
}

func ownershipParserRelocationFailures(t *testing.T, p *fixture.Pair, c *sql.Conn, run, reject func(fixture.Step), id int64) {
	t.Helper()
	queries := []string{
		ownershipClassMemberSet, ownershipEdges, ownershipProcedures,
		`SELECT oid::bigint,extnamespace::bigint FROM pg_extension WHERE extname='unaccent'`,
		`SELECT oid::bigint,pronamespace::bigint FROM pg_proc WHERE pronamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) ORDER BY oid`,
		`SELECT oid::bigint,dictnamespace::bigint FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) ORDER BY oid`,
		`SELECT oid::bigint,tmplnamespace::bigint FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) ORDER BY oid`,
		`SELECT oid::bigint,prsnamespace::bigint FROM pg_ts_parser WHERE oid=` + fixture.OwnershipNumber(id),
	}
	capture := func() [][][]any {
		var out [][][]any
		for _, q := range queries {
			out = append(out, fixture.OwnershipSnapshot(t, p, c, q))
		}
		return out
	}
	unchanged := func(before [][][]any) {
		for i, q := range queries {
			fixture.OwnershipUnchanged(t, p, c, q, before[i])
		}
		ownershipParserWitness(run, "eo_old")
	}
	run(fixture.Exec(ownershipParserCreate("eo_new.item"), 0))
	before := capture()
	reject(fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "42710", `text search parser "item" already exists in schema "eo_new"`))
	unchanged(before)
	run(fixture.Exec(`DROP TEXT SEARCH PARSER eo_new.item`, 0))
	run(fixture.Exec(`ALTER TEXT SEARCH PARSER eo_old.item SET SCHEMA eo_other`, 0))
	before = capture()
	s := fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "0A000", `extension "unaccent" does not support SET SCHEMA`)
	s.Diagnostic.Detail = `text search parser eo_other.item is not in the extension's schema "eo_old"`
	reject(s)
	for i, q := range queries {
		fixture.OwnershipUnchanged(t, p, c, q, before[i])
	}
	ownershipParserWitness(run, "eo_other")
	run(fixture.Exec(`ALTER TEXT SEARCH PARSER eo_other.item SET SCHEMA eo_old`, 0))
}

func ownershipParserOldRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 2 || columns[0].Name() != "tokid" || columns[0].DatabaseTypeName() != "INT4" || columns[1].Name() != "token" || columns[1].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained parser metadata", columns, err)
	}
	var tokid int64
	var token string
	if !rows.Next() {
		t.Fatal("lost retained parser row", rows.Err())
	}
	if err := rows.Scan(&tokid, &token); err != nil || tokid != 1 || token != "Word" {
		t.Fatal("retained parser value", tokid, token, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained parser EOF", rows.Err())
	}
}
