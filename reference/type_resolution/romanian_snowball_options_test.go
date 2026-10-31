package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRomanianSnowballOptions(t *testing.T) { romanianOptions(t) }

func romanianOptions(t *testing.T) {
	t.Run("configured-english-stoplist", romanianEnglishStoplist)
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY romanian_options (TEMPLATE=pg_catalog.snowball,Language='RoMaNiAn',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION romanian_options_config (COPY=romanian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian_options_config ALTER MAPPING FOR asciiword WITH romanian_options", 0)
		q := "SELECT ts_lexize('romanian_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "THE")
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{ocol}", "OCOLUL")
		dictionaryResult(t, p, b, held, q, "{abrupt}", "abruptă")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RUNNING') AS lexemes", "{run}")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'RoMaNiAn'", "{only}"},
			{"dummy", "language = 'RoMaNiAn'", "{only}"},
			{"stopwords=''", "language = 'RoMaNiAn', stopwords = ''", "{only}"},
			{"language=romanian,language='RoMaNiAn'", "stopwords = '', language = 'RoMaNiAn'", "{only}"},
			{"stopwords=english", "language = 'RoMaNiAn', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			the, being := "{the}", "{being}"
			if tc.only == "{}" {
				the, being = "{}", "{}"
			}
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, the, "THE")
				dictionaryResult(t, p, b, e, q, being, "being")
			}
			dictionaryResult(t, p, b, c, q, "{ocol}", "OCOLUL")
		}
		dictionaryResult(t, p, b, held, q, "{abrupt}", "abruptă")
		preserved := romanianDefinitionSnapshot(t, p, b, c, "romanian_options", "romanian_options_config")
		romanianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_options (language=english,stopwords=romanian)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'RoMaNiAn', stopwords = 'english'"}})
		dictionaryResult(t, p, b, held, q, "{abrupt}", "abruptă")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='romanian_options'"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ opt, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=romanian_bogus", "42704", `no Snowball stemmer available for language "romanian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_options ("+tc.opt+")", tc.code, tc.message)
			preserved()
			dictionaryResult(t, p, b, held, q, "{abrupt}", "abruptă")
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{ocol}", "OCOLUL")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('romanian_options_config','OCOLUL ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ocol':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='romanian_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='romanian_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func TestReferenceRomanianSnowballDiagnostics(t *testing.T) {
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=romanian,language=romanian", "22023", "multiple Language parameters"},
		{"language=romanian,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=romanian", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=romanian`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=romanian_bogus,language=romanian", "42704", `no Snowball stemmer available for language "romanian_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' romanian '", "42704", `no Snowball stemmer available for language " romanian " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=romanian,stopwords='../romanian'", "22023", `invalid text search configuration file name "../romanian"`},
		{"language=romanian,stopwords='ROMANIAN'", "22023", `invalid text search configuration file name "ROMANIAN"`},
		{"language=romanian,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for i, tc := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY romanian_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='romanian_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, input := range []any{string([]byte{0xff}), []byte{0xff}, "ա\x00", []byte{'a', 0}} {
			suffix := "0x00"
			switch v := input.(type) {
			case string:
				if len(v) == 1 {
					suffix = "0xff"
				}
			case []byte:
				if len(v) == 1 {
					suffix = "0xff"
				}
			}
			d := dictionaryDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+suffix)
			d.Context = "unnamed portal parameter $2"
			s := fixture.ErrorDiagnostic(q, d, "romanian_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=romanian", "language=romanian,stopwords=romanian,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY romanian_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "romanian.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "romanian.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'abruptă')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('romanian_stem',1)", "unknown, integer", "8"},
			{"SELECT 'բ' AS prefix, ts_lexize(true,'abruptă')", "boolean, unknown", "23"},
		} {
			d := dictionaryDiagnostic("42883", "function ts_lexize("+tc.signature+") does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = tc.position
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		missing := dictionaryDiagnostic("42704", `text search dictionary "romanian_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('romanian_absent','ocol')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "romanian_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('romanian_absent','abruptă')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY romanian_prefix (TEMPLATE=snowball,Language=romanian) "
		d := dictionaryDiagnostic("42601", `syntax error at or near "invalid_suffix"`)
		d.Position = fixture.Position(len(prefix) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(prefix+"invalid_suffix", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('romanian_prefix','romanian_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceRomanianSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.romanian_owned (TEMPLATE=pg_catalog.snowball,Language=romanian,StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION dictionary_owned.romanian_owned_config (COPY=romanian)", "ALTER TEXT SEARCH CONFIGURATION dictionary_owned.romanian_owned_config ALTER MAPPING FOR asciiword,word WITH dictionary_owned.romanian_owned", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		preserved := romanianDefinitionSnapshot(t, p, b, c, "dictionary_owned.romanian_owned", "dictionary_owned.romanian_owned_config")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.romanian_denied (TEMPLATE=snowball,Language=romanian)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.romanian_denied (TEMPLATE=snowball,Language=romanian)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY romanian_missing_schema.denied (TEMPLATE=snowball,Language=romanian)", "3F000", `schema "romanian_missing_schema" does not exist`)
		preserved()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='romanian_denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.romanian_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.romanian_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.romanian_owned")
			preserved()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.romanian_owned','ONLY') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.romanian_owned','OCOLUL') AS lexemes", "{ocol}")
		regtsSelect(t, p, b, c, "SELECT to_tsvector('dictionary_owned.romanian_owned_config','ONLY ocolul') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ocol':2"}})
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.romanian_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'romanian', stopwords = 'english'"}})
	})
}
