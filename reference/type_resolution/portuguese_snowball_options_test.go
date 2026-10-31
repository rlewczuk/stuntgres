package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePortugueseSnowballOptions(t *testing.T) {
	t.Run("diagnostics", portugueseDiagnostics)
	t.Run("ownership", portugueseSnowballOwnership)
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY portuguese_options (TEMPLATE=pg_catalog.snowball,Language='PoRtUgUeSe',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION portuguese_options_config (COPY=portuguese)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION portuguese_options_config ALTER MAPPING FOR asciiword,word WITH portuguese_options", 0)
		q := "SELECT ts_lexize('portuguese_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "THE")
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{running}", "RUNNING")
		dictionaryResult(t, p, b, held, q, "{cas}", "casas")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RUNNING') AS lexemes", "{run}")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'PoRtUgUeSe'", "{only}"},
			{"dummy", "language = 'PoRtUgUeSe'", "{only}"},
			{"stopwords=''", "language = 'PoRtUgUeSe', stopwords = ''", "{only}"},
			{"language=portuguese,language='PoRtUgUeSe'", "stopwords = '', language = 'PoRtUgUeSe'", "{only}"},
			{"stopwords=english", "language = 'PoRtUgUeSe', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='portuguese_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			dictionaryResult(t, p, b, c, q, "{running}", "RUNNING")
		}
		dictionaryResult(t, p, b, held, q, "{cas}", "casas")
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "portuguese_options", "portuguese_options_config")
		portugueseMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_options (language=english,stopwords=portuguese_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='portuguese_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'PoRtUgUeSe', stopwords = 'english'"}})
		dictionaryResult(t, p, b, held, q, "{cas}", "casas")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='portuguese_options'"
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
			{"stopwords,language=portuguese_bogus", "42704", `no Snowball stemmer available for language "portuguese_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_options ("+tc.opt+")", tc.code, tc.message)
			preserved()
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{running}", "RUNNING")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('portuguese_options_config','RUNNING ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'running':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='portuguese_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='portuguese_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func portugueseDiagnostics(t *testing.T) {
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=portuguese,language=portuguese", "22023", "multiple Language parameters"},
		{"language=portuguese,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=portuguese", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=portuguese`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=portuguese_bogus,language=portuguese", "42704", `no Snowball stemmer available for language "portuguese_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' portuguese '", "42704", `no Snowball stemmer available for language " portuguese " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=portuguese,stopwords='../portuguese'", "22023", `invalid text search configuration file name "../portuguese"`},
		{"language=portuguese,stopwords='Portuguese'", "22023", `invalid text search configuration file name "Portuguese"`},
		{"language=portuguese,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
		{"language=portuguese,stopwords='PORTUGUESE'", "22023", `invalid text search configuration file name "PORTUGUESE"`},
	}
	for i, tc := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY portuguese_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='portuguese_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, input := range []any{string([]byte{0xff}), []byte{0xff}, "å\x00", []byte{'a', 0}} {
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
			s := fixture.ErrorDiagnostic(q, d, "portuguese_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=portuguese_missing", "language=portuguese,stopwords=portuguese_missing,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY portuguese_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "portuguese_missing.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "portuguese_missing.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'casas')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('portuguese_stem',1)", "unknown, integer", "8"},
			{"SELECT ts_lexize('portuguese_stem')", "unknown", "8"},
			{"SELECT ts_lexize('portuguese_stem','casas','extra')", "unknown, unknown, unknown", "8"},
			{"SELECT 'à' AS prefix, ts_lexize(true,'casas')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "portuguese_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('portuguese_absent','rats')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "portuguese_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('portuguese_absent','casas')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY portuguese_no_template (TEMPLATE=portuguese_absent,Language=portuguese)", "42704", `text search template "portuguese_absent" does not exist`)
		for _, prepared := range []bool{false, true} {
			d := dictionaryDiagnostic("42602", "invalid name syntax")
			d.Position = "18"
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('a..b','casas')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY portuguese_prefix (TEMPLATE=snowball,Language=portuguese) "
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
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('portuguese_prefix','portuguese_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func portugueseSnowballOwnership(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.portuguese_owned (TEMPLATE=pg_catalog.snowball,Language=portuguese,StopWords=english)", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "dictionary_owned.portuguese_owned", "portuguese")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.portuguese_denied (TEMPLATE=snowball,Language=portuguese)", "42501", "permission denied for schema dictionary_owned")
		preserved()
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.portuguese_denied (TEMPLATE=snowball,Language=portuguese)", "42501", "permission denied for schema pg_catalog")
		preserved()
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY portuguese_missing_schema.denied (TEMPLATE=snowball,Language=portuguese)", "3F000", `schema "portuguese_missing_schema" does not exist`)
		preserved()
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.portuguese_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.portuguese_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.portuguese_owned")
			preserved()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.portuguese_owned','ONLY') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.portuguese_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'portuguese', stopwords = 'english'"}})
	})
}
