package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func yiddishDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('yiddish','קינדער RATS 123')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"word", "Word, all letters", "קינדער", "{yiddish_stem}", "yiddish_stem", "{קינד}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"asciiword", "Word, all ASCII", "RATS", "{yiddish_stem}", "yiddish_stem", "{rats}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"uint", "Unsigned integer", "123", "{simple}", "simple", "{123}"},
	})
}

func TestReferenceYiddishSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aliases := []string{"asciiword", "word", "numword", "email", "url", "host", "sfloat", "version", "hword_numpart", "hword_part", "hword_asciipart", "blank", "tag", "protocol", "numhword", "asciihword", "hword", "url_path", "file", "float", "int", "uint", "entity"}
		var tokens, maps [][]any
		for i, alias := range aliases {
			id := int64(i + 1)
			tokens = append(tokens, []any{id, alias})
			switch id { case 12, 13, 14, 23: continue }
			dict := "simple"
			switch id { case 1, 2, 10, 11, 16, 17: dict = "yiddish_stem" }
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'yiddish_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='yiddish'::regconfig ORDER BY maptokentype,mapseqno"
		lithuanianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, step := range []fixture.Step{
			yiddishDebugStep(),
			fixture.Select("SELECT to_tsvector('yiddish','קינדער RATS 123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':3 'rats':2 'קינד':1"}}),
			fixture.Select("SELECT to_tsvector('yiddish','ONLY THE און') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'the':2 'אונ':3"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('yiddish',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('yiddish','קינדער') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('yiddish','קינדער') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} { lithuanianSmokeBoth(t, p, b, c, step) }
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION yiddish_copy (COPY=yiddish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION yiddish_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		regtsSelect(t, p, b, c, "SELECT to_tsvector('yiddish_copy','RUNNING קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':1 'קינד':2"}})
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_map (TEMPLATE=snowball,Language=yiddish,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION yiddish_copy ALTER MAPPING FOR asciiword,word WITH yiddish_map", 0)
		regtsSelect(t, p, b, c, "SELECT to_tsvector('yiddish_copy','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'קינד':2"}})
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "yiddish_map", "yiddish_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION yiddish_copy ALTER MAPPING FOR asciiword,yiddish_bad_token WITH simple", "22023", `token type "yiddish_bad_token" does not exist`)
		preserved()
		lithuanianSmokeBoth(t, p, b, c, yiddishDebugStep())
		yiddishConfigurationSettings(t, p, b, c)
	})
}

func TestReferenceYiddishSnowballIdentity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dict, template, init, lexize, config int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE dictname='yiddish_stem' AND dictnamespace='pg_catalog'::regnamespace").Scan(&dict, &template); err != nil { t.Fatal(err) }
		if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=$1::oid AND tmplname='snowball' AND tmplnamespace='pg_catalog'::regnamespace", template).Scan(&init, &lexize); err != nil { t.Fatal(err) }
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='yiddish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&config); err != nil { t.Fatal(err) }
		// OIDs are class-qualified; the allocator may legally reuse an OID in
		// another catalog. Only helper functions share a class with each other.
		if dict <= 0 || template <= 0 || config <= 0 || init <= 0 || lexize <= 0 || init == lexize { t.Fatal(dict, template, config, init, lexize) }
		for _, name := range []string{"simple", "english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "portuguese"} {
			dictionary := name + "_stem"
			if name == "simple" { dictionary = name }
			var d, cfg int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint,$2::regconfig::oid::bigint", dictionary, name).Scan(&d, &cfg); err != nil { t.Fatal(err) }
			if d <= 0 || cfg <= 0 || d == dict || cfg == config { t.Fatal("seed alias", name, d, cfg) }
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption,dictnamespace::bigint,dictowner::bigint,dicttemplate=(SELECT dicttemplate FROM pg_ts_dict WHERE oid='english_stem'::regdictionary) AS shared FROM pg_ts_dict WHERE oid=$1::oid", []string{"dictinitoption", "dictnamespace", "dictowner", "shared"}, []string{"TEXT", "INT8", "INT8", "BOOL"}, [][]any{{"language = 'yiddish'", int64(11), int64(10), true}}, dict)
		regtsSelect(t, p, b, c, "SELECT cfgnamespace::bigint,cfgowner::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=$1::oid", []string{"cfgnamespace", "cfgowner", "cfgparser"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(11), int64(10), int64(3722)}}, config)
		for _, tc := range []struct { id int64; name, args string; count int64 }{{init, "dsnowball_init", "2281", 1}, {lexize, "dsnowball_lexize", "2281 2281 2281 2281", 4}} {
			regtsSelect(t, p, b, c, "SELECT proname,proargtypes::text AS args,pronargs::bigint AS nargs,prorettype::bigint,proisstrict,provolatile,proparallel,prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS c_language FROM pg_proc WHERE oid=$1::oid", []string{"proname", "args", "nargs", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "probin", "c_language"}, []string{"NAME", "TEXT", "INT8", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "BOOL"}, [][]any{{tc.name, tc.args, tc.count, int64(2281), true, "v", "u", tc.name, "$libdir/dict_snowball", true}}, tc.id)
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT count(*) AS n FROM pg_ts_template WHERE tmplname='snowball' AND tmplnamespace=11", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace=11 AND proname IN ('dsnowball_init','dsnowball_lexize')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid='yiddish_stem'::regdictionary AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		} { lithuanianSmokeBoth(t, p, b, c, step) }
	})
}
