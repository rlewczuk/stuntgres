package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSerbianSnowballOptions(t *testing.T) {
	t.Run("configured-english-stoplist", serbianEnglishStoplist)
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY serbian_options (TEMPLATE=pg_catalog.snowball,Language='SeRbIaN',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION serbian_options_config (COPY=serbian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION serbian_options_config ALTER MAPPING FOR asciiword WITH serbian_options", 0)
		q := "SELECT ts_lexize('serbian_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "THE")
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{abakus}", "ABAKUSA")
		dictionaryResult(t, p, b, held, q, "{rad}", "рад")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RUNNING') AS lexemes", "{run}")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'SeRbIaN'", "{only}"},
			{"dummy", "language = 'SeRbIaN'", "{only}"},
			{"stopwords=''", "language = 'SeRbIaN', stopwords = ''", "{only}"},
			{"language=serbian,language='SeRbIaN'", "stopwords = '', language = 'SeRbIaN'", "{only}"},
			{"stopwords=english", "language = 'SeRbIaN', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='serbian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			the, being := "{the}", "{being}"
			if tc.only == "{}" {
				the, being = "{}", "{}"
			}
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, the, "THE")
				dictionaryResult(t, p, b, e, q, being, "being")
			}
			dictionaryResult(t, p, b, c, q, "{abakus}", "ABAKUSA")
		}
		dictionaryResult(t, p, b, held, q, "{rad}", "рад")
		preserved := serbianDefinitionSnapshot(t, p, b, c, "serbian_options", "serbian_options_config")
		serbianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_options (language=english,stopwords=serbian)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='serbian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'SeRbIaN', stopwords = 'english'"}})
		dictionaryResult(t, p, b, held, q, "{rad}", "рад")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='serbian_options'"
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
			{"stopwords,language=serbian_bogus", "42704", `no Snowball stemmer available for language "serbian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_options ("+tc.opt+")", tc.code, tc.message)
			preserved()
			dictionaryResult(t, p, b, held, q, "{rad}", "рад")
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{abakus}", "ABAKUSA")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('serbian_options_config','ABAKUSA ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abakus':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='serbian_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='serbian_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func TestReferenceSerbianSnowballDiagnostics(t *testing.T) {
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=serbian,language=serbian", "22023", "multiple Language parameters"},
		{"language=serbian,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=serbian", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=serbian`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=serbian_bogus,language=serbian", "42704", `no Snowball stemmer available for language "serbian_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' serbian '", "42704", `no Snowball stemmer available for language " serbian " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=serbian,stopwords='../serbian'", "22023", `invalid text search configuration file name "../serbian"`},
		{"language=serbian,stopwords='SERBIAN'", "22023", `invalid text search configuration file name "SERBIAN"`},
		{"language=serbian,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for i, tc := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY serbian_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='serbian_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
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
			s := fixture.ErrorDiagnostic(q, d, "serbian_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=serbian", "language=serbian,stopwords=serbian,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY serbian_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "serbian.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "serbian.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'рад')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('serbian_stem',1)", "unknown, integer", "8"},
			{"SELECT 'բ' AS prefix, ts_lexize(true,'рад')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "serbian_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('serbian_absent','abakus')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "serbian_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('serbian_absent','рад')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY serbian_prefix (TEMPLATE=snowball,Language=serbian) "
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
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('serbian_prefix','serbian_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceSerbianSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.serbian_owned (TEMPLATE=pg_catalog.snowball,Language=serbian,StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION dictionary_owned.serbian_owned_config (COPY=serbian)", "ALTER TEXT SEARCH CONFIGURATION dictionary_owned.serbian_owned_config ALTER MAPPING FOR asciiword,word WITH dictionary_owned.serbian_owned", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		preserved := serbianDefinitionSnapshot(t, p, b, c, "dictionary_owned.serbian_owned", "dictionary_owned.serbian_owned_config")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.serbian_denied (TEMPLATE=snowball,Language=serbian)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.serbian_denied (TEMPLATE=snowball,Language=serbian)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY serbian_missing_schema.denied (TEMPLATE=snowball,Language=serbian)", "3F000", `schema "serbian_missing_schema" does not exist`)
		preserved()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='serbian_denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.serbian_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.serbian_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.serbian_owned")
			preserved()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.serbian_owned','ONLY') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.serbian_owned','ABAKUSA') AS lexemes", "{abakus}")
		regtsSelect(t, p, b, c, "SELECT to_tsvector('dictionary_owned.serbian_owned_config','ONLY abakusa') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abakus':2"}})
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.serbian_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'serbian', stopwords = 'english'"}})
	})
}