package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

const parserDDLMethods = "START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end,LEXTYPES=pg_catalog.prsd_lextype"
const parserDDLDocument = "Word 123 foo@example.com http://example.com/stuff/index.html !"

func parserDDLCreate(name string) string {
	return "CREATE TEXT SEARCH PARSER " + name + " (" + parserDDLMethods + ")"
}
func parserDDLCallbacks(name string, headline int64) fixture.Step {
	return fixture.Select("SELECT prsstart::oid::bigint,prstoken::oid::bigint,prsend::oid::bigint,prsheadline::oid::bigint,prslextype::oid::bigint FROM pg_ts_parser WHERE prsname=$1", []string{"prsstart", "prstoken", "prsend", "prsheadline", "prslextype"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3717), int64(3718), int64(3719), headline, int64(3721)}}, name)
}
func parserDDLTokens(name string) fixture.Step {
	texts := []string{"Word", " ", "123", " ", "foo@example.com", " ", "http://", "example.com/stuff/index.html", "example.com", "/stuff/index.html", " !"}
	ids := []int64{1, 12, 22, 12, 4, 12, 14, 5, 6, 18, 12}
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id, texts[i], int64(i + 1)}
	}
	return fixture.Select("SELECT tokid,token,ordinality FROM ts_parse($1::text,$2::text) WITH ORDINALITY ORDER BY ordinality", []string{"tokid", "token", "ordinality"}, []string{"INT4", "TEXT", "INT8"}, rows, name, parserDDLDocument)
}
func parserDDLWord(name string) fixture.Step {
	return fixture.Select("SELECT * FROM ts_parse($1::text,'Word')", []string{"tokid", "token"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Word"}}, name)
}
func parserDDLRestrict(name, config string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop text search parser "+name+" because other objects depend on it")
	d.Detail = "text search configuration " + config + " depends on text search parser " + name
	d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return fixture.ErrorDiagnostic("DROP TEXT SEARCH PARSER "+name+" RESTRICT", d)
}
func parserDDLConfigure(name, parser string) []string {
	return []string{"CREATE TEXT SEARCH CONFIGURATION " + name + " (PARSER=" + parser + ")", "ALTER TEXT SEARCH CONFIGURATION " + name + " ADD MAPPING FOR asciiword WITH pg_catalog.simple"}
}

func TestReferenceFTSParserDDLCreate(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, prepared := range []bool{false, true} {
			for i, options := range []string{parserDDLMethods, parserDDLMethods + ",HEADLINE=pg_catalog.prsd_headline", "LEXTYPES=pg_catalog.prsd_lextype,END=pg_catalog.prsd_end,GETTOKEN=pg_catalog.prsd_nexttoken,START=pg_catalog.prsd_start", parserDDLMethods + ",START=pg_catalog.prsd_start"} {
				name := fmt.Sprintf("parser_ddl_create_%d", i)
				s := fixture.Exec("CREATE TEXT SEARCH PARSER public."+name+" ("+options+")", 0)
				s.Prepared = prepared
				run(s)
				headline := int64(0)
				if i == 1 {
					headline = 3720
				}
				run(parserDDLCallbacks(name, headline))
				run(parserDDLWord("public." + name))
				run(fixture.Exec("DROP TEXT SEARCH PARSER public."+name, 0))
			}
		}
	})
}
func TestReferenceFTSParserDDLExecution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "parser_ddl_path")
	fixture.SchemaReferenceCleanup(t, "Parser DDL")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(parserDDLCreate("public.parser_ddl"), 0))
		for _, s := range []fixture.Step{parserDDLTokens("public.parser_ddl"), fixture.Select("SELECT * FROM ts_token_type('public.parser_ddl') ORDER BY tokid", []string{"tokid", "alias", "description"}, []string{"INT4", "TEXT", "TEXT"}, ftsParserDescriptors()), fixture.Select("SELECT ts_parse('public.parser_ddl','Word')", []string{"ts_parse"}, []string{"RECORD"}, [][]any{{"(1,Word)"}}), fixture.Select("SELECT * FROM ts_parse((SELECT oid FROM pg_ts_parser WHERE prsname='parser_ddl'),'Word')", []string{"tokid", "token"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Word"}})} {
			run(s)
			run(fixture.Prepared(s))
		}
		for _, q := range parserDDLConfigure("public.parser_config", "public.parser_ddl") {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.parser_copy (COPY=public.parser_config)", 0))
		for _, name := range []string{"public.parser_config", "public.parser_copy"} {
			s := ftsConfigurationVector("SELECT to_tsvector($1::regconfig,'Word') AS normalized", "'word':1", name)
			run(s)
			run(fixture.Prepared(s))
		}
		held, err := c.PrepareContext(p.Ctx, "SELECT * FROM ts_parse($1::text,$2::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(name any, text any, want int) {
			t.Helper()
			rows, err := held.QueryContext(p.Ctx, name, text)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal("held shape", types, err)
			}
			count := 0
			for rows.Next() {
				count++
			}
			if rows.Err() != nil || count != want {
				t.Fatal("held rows", count, rows.Err())
			}
		}
		check("public.parser_ddl", "Word", 1)
		check("public.parser_ddl", "", 0)
		check(nil, "Word", 0)
		check("public.parser_ddl", nil, 0)
		run(fixture.Exec("ALTER TEXT SEARCH PARSER public.parser_ddl RENAME TO parser_ddl_current", 0))
		rows, err := held.QueryContext(p.Ctx, "public.parser_ddl", "Word")
		if rows != nil {
			rows.Close()
		}
		d := fixture.ControlsReferenceDiagnostic("42704", `text search parser "public.parser_ddl" does not exist`)
		fixture.AssertDiagnostic(t, err, &d)
		check("public.parser_ddl_current", "Word", 1)
		run(fixture.Exec("CREATE SCHEMA parser_ddl_path", 0))
		run(fixture.Exec(parserDDLCreate("parser_ddl_path.parser_ddl_current"), 0))
		run(fixture.Exec("SET search_path=parser_ddl_path,public", 0))
		run(parserDDLWord("parser_ddl_current"))
		run(fixture.Exec(`CREATE SCHEMA "Parser DDL"`, 0))
		run(fixture.Exec(parserDDLCreate(`"Parser DDL"."Quoted Parser"`), 0))
		run(parserDDLWord(`"Parser DDL"."Quoted Parser"`))
		run(fixture.Exec(parserDDLCreate("pg_temp.parser_ddl_current"), 0))
		run(fixture.Exec("SET search_path=pg_temp,public", 0))
		run(parserDDLWord("parser_ddl_current"))
	})
}
func TestReferenceFTSParserDDLAlterDrop(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "parser_ddl_move")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA parser_ddl_move", 0))
		run(fixture.Exec(parserDDLCreate("public.parser_ddl"), 0))
		for _, q := range parserDDLConfigure("public.parser_config", "public.parser_ddl") {
			run(fixture.Exec(q, 0))
		}
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_parser WHERE prsname='parser_ddl'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH PARSER public.parser_ddl RENAME TO parser_renamed", 0)))
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH PARSER public.parser_renamed SET SCHEMA parser_ddl_move", 0)))
		run(fixture.Select("SELECT oid::bigint FROM pg_ts_parser WHERE prsname='parser_renamed'", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}))
		run(parserDDLTokens("parser_ddl_move.parser_renamed"))
		run(parserDDLCallbacks("parser_renamed", 0))
		run(ftsConfigurationError("SELECT * FROM ts_parse('public.parser_ddl','Word')", "42704", `text search parser "public.parser_ddl" does not exist`))
		run(ftsConfigurationVector("SELECT to_tsvector('public.parser_config','Word') AS normalized", "'word':1"))
		run(fixture.Exec(parserDDLCreate("public.parser_first"), 0))
		missing := ftsConfigurationError("DROP TEXT SEARCH PARSER public.parser_first,public.parser_missing", "42704", `text search parser "public.parser_missing" does not exist`)
		run(missing)
		run(fixture.Prepared(missing))
		run(parserDDLWord("public.parser_first"))
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop desired object(s) because other objects depend on them")
		d.Detail = "text search configuration parser_config depends on text search parser parser_ddl_move.parser_renamed"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		dependent := fixture.ErrorDiagnostic("DROP TEXT SEARCH PARSER public.parser_first,parser_ddl_move.parser_renamed RESTRICT", d)
		run(dependent)
		run(fixture.Prepared(dependent))
		run(parserDDLWord("public.parser_first"))
		run(fixture.Exec("DROP TEXT SEARCH PARSER parser_ddl_move.parser_renamed CASCADE", 0))
		run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname='parser_config'", []string{"cfgname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE oid IN (3717,3718,3719,3720,3721)", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
		defer parserDDLRestoreDefault(t, c)
		run(fixture.Exec("ALTER TEXT SEARCH PARSER pg_catalog.default RENAME TO parser_seed", 0))
		run(fixture.Exec("ALTER TEXT SEARCH PARSER pg_catalog.parser_seed SET SCHEMA parser_ddl_move", 0))
		// Always return the pinned identity to its baseline before fixture cleanup.
		run(parserDDLTokens("parser_ddl_move.parser_seed"))
		run(ftsConfigurationError("SELECT * FROM ts_parse('default','Word')", "42704", `text search parser "default" does not exist`))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.parser_seed_config (PARSER=parser_ddl_move.parser_seed)", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('pg_catalog.simple','Word') AS normalized", "'word':1"))
		run(ftsConfigurationError("DROP TEXT SEARCH PARSER parser_ddl_move.parser_seed", "2BP01", "cannot drop text search parser parser_ddl_move.parser_seed because it is required by the database system"))
	})
}
func TestReferenceFTSParserDDLMetadata(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(parserDDLCreate("public.parser_ddl"), 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.parser_config (PARSER=public.parser_ddl)", 0))
		for _, s := range []fixture.Step{parserDDLCallbacks("parser_ddl", 0), fixture.Select("SELECT oid,prsname,prsnamespace,prsstart,prstoken,prsend,prsheadline,prslextype FROM pg_ts_parser WHERE FALSE", []string{"oid", "prsname", "prsnamespace", "prsstart", "prstoken", "prsend", "prsheadline", "prslextype"}, []string{"OID", "NAME", "OID", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC"}, nil), fixture.Select("SELECT prsheadline IS NULL AS absent,prsheadline::oid=0 AS zero FROM pg_ts_parser WHERE prsname='parser_ddl'", []string{"absent", "zero"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}), fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_ts_parser p ON d.objid=p.oid WHERE d.classid='pg_ts_parser'::regclass AND p.prsname='parser_ddl' AND d.refclassid='pg_proc'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}), fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_ts_parser p ON p.oid=d.objid WHERE d.classid='pg_ts_parser'::regclass AND p.prsname='parser_ddl' AND d.refclassid='pg_namespace'::regclass AND d.refobjid='public'::regnamespace", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}), fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_ts_parser p ON p.oid=d.refobjid WHERE d.refclassid='pg_ts_parser'::regclass AND p.prsname='parser_ddl' AND d.classid='pg_ts_config'::regclass AND d.objid='public.parser_config'::regconfig::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}), fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='pg_ts_parser'::regclass AND attnum>0 AND attname IN ('prsowner','prsacl')", []string{"attname"}, []string{"NAME"}, nil)} {
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
func TestReferenceFTSParserDDLFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, prepared := range []bool{false, true} {
			name := "parser_ddl_smoke"
			config := name + "_cfg"
			s := fixture.Exec(parserDDLCreate("public."+name), 0)
			s.Prepared = prepared
			run(s)
			run(parserDDLCallbacks(name, 0))
			s = parserDDLTokens("public." + name)
			s.Prepared = prepared
			run(s)
			heldSQL := "SELECT * FROM ts_parse($1::text,$2::text)"
			statement, err := c.PrepareContext(p.Ctx, heldSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: heldSQL}
			heldWord := func(parser any, text any, rows [][]any) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldSQL, []string{"tokid", "token"}, []string{"INT4", "TEXT"}, rows, parser, text))
			}
			heldWord("public."+name, "Word", [][]any{{int64(1), "Word"}})
			heldWord("public."+name, "", nil)
			heldWord(nil, "Word", nil)
			for _, q := range parserDDLConfigure("public."+config, "public."+name) {
				s = fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
			}
			run(ftsConfigurationVector("SELECT to_tsvector('public."+config+"','Word') AS normalized", "'word':1"))
			s = fixture.Exec("ALTER TEXT SEARCH PARSER public."+name+" RENAME TO parser_ddl_smoke_new", 0)
			s.Prepared = prepared
			run(s)
			parserDDLSmokeControls(t, p, b, c)
			missing := fixture.ErrorDiagnostic(heldSQL, fixture.ControlsReferenceDiagnostic("42704", `text search parser "public.parser_ddl_smoke" does not exist`), "public.parser_ddl_smoke", "Word")
			missing.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, missing)
			heldWord("public.parser_ddl_smoke_new", "Word", [][]any{{int64(1), "Word"}})
			run(parserDDLWord("public.parser_ddl_smoke_new"))
			restricted := parserDDLRestrict("parser_ddl_smoke_new", config)
			restricted.Prepared = prepared
			run(restricted)
			run(parserDDLWord("public.parser_ddl_smoke_new"))
			s = fixture.Exec("DROP TEXT SEARCH PARSER public.parser_ddl_smoke_new CASCADE", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname=$1", []string{"cfgname"}, []string{"NAME"}, nil, config))
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
