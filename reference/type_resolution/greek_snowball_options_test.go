package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGreekSnowballOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_options (TEMPLATE=pg_catalog.snowball,Language='GrEeK',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION greek_options_config (COPY=greek)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION greek_options_config ALTER MAPPING FOR asciiword WITH greek_options", 0)
		q := "SELECT ts_lexize('greek_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "THE")
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{rats}", "RATS")
		dictionaryResult(t, p, b, held, q, "{καλ}", "καλός")
		for _, executor := range []fixture.Executor{c, held} {
			dictionaryResult(t, p, b, executor, q, "{δανει}", "δανείζω")
			dictionaryResult(t, p, b, executor, q, "{καλ}", "καλός")
			dictionaryResult(t, p, b, executor, q, "{rats}", "RATS")
			dictionaryResult(t, p, b, executor, q, "{}", "ONLY")
			dictionaryResult(t, p, b, executor, q, "{}", "THE")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RATS') AS lexemes", "{rat}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options (language=english)", 0)
		dictionaryResult(t, p, b, held, q, "{rat}", "RATS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options (language='GrEeK')", 0)
		dictionaryResult(t, p, b, held, q, "{rats}", "RATS")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'GrEeK'", "{only}"},
			{"dummy", "language = 'GrEeK'", "{only}"},
			{"stopwords=''", "language = 'GrEeK', stopwords = ''", "{only}"},
			{"language=greek,language='GrEeK'", "stopwords = '', language = 'GrEeK'", "{only}"},
			{"stopwords=english", "language = 'GrEeK', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='greek_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			dictionaryResult(t, p, b, c, q, "{rats}", "RATS")
		}
		dictionaryResult(t, p, b, held, q, "{καλ}", "καλός")
		greekMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options (language=english,stopwords=greek)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='greek_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'GrEeK', stopwords = 'english'"}})
		dictionaryResult(t, p, b, held, q, "{καλ}", "καλός")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='greek_options'"
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
			{"stopwords,language=greek_bogus", "42704", `no Snowball stemmer available for language "greek_bogus" and encoding "UTF8"`},
			{"language=english,stopwords=greek", "F0000", `could not open stop-word file "greek.stop": No such file or directory`},
		} {
			if tc.code == "F0000" {
				greekMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options ("+tc.opt+")")
			} else {
				dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_options ("+tc.opt+")", tc.code, tc.message)
			}
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{rats}", "RATS")
			dictionaryResult(t, p, b, held, q, "{καλ}", "καλός")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('greek_options_config','RATS ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='greek_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='greek_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func TestReferenceGreekSnowballDiagnostics(t *testing.T) {
	t.Run("ownership", greekSnowballOwnership)
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=greek,language=greek", "22023", "multiple Language parameters"},
		{"language=greek,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=greek", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=greek`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=greek_bogus,language=greek", "42704", `no Snowball stemmer available for language "greek_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' greek '", "42704", `no Snowball stemmer available for language " greek " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=greek,stopwords='../greek'", "22023", `invalid text search configuration file name "../greek"`},
		{"language=greek,stopwords='ENGLISH'", "22023", `invalid text search configuration file name "ENGLISH"`},
		{"language=greek,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for i, tc := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				var before bytes.Buffer
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
				}
				var dict, template, owner, namespace, config int64
				var initial string
				const snapshot = "SELECT oid::bigint,dicttemplate::bigint,dictowner::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='greek_stem'::regdictionary"
				if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&dict, &template, &owner, &namespace, &initial); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT 'greek'::regconfig::oid::bigint").Scan(&config); err != nil {
					t.Fatal(err)
				}
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='greek_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				regtsSelect(t, p, b, c, snapshot, []string{"oid", "dicttemplate", "dictowner", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{dict, template, owner, namespace, initial}})
				regtsSelect(t, p, b, c, "SELECT 'greek'::regconfig::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{config}})
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='greek'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
				regtsSelect(t, p, b, c, "SELECT to_tsvector('greek','THE καλός κρέατα') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'the':1 'καλ':2 'κρε':3"}})
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('greek_stem','δανείζω') AS lexemes", "{δανει}")
				if !b.PG {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("failed CREATE changed whole catalog", err)
					}
				}
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
		for _, input := range []any{string([]byte{0xff}), []byte{0xff}, "α\x00", []byte{'a', 0}} {
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
			s := fixture.ErrorDiagnostic(q, d, "greek_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=greek", "language=greek,stopwords=greek,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY greek_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "greek.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "greek.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'καλός')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('greek_stem',1)", "unknown, integer", "8"},
			{"SELECT ts_lexize('greek_stem')", "unknown", "8"},
			{"SELECT ts_lexize('greek_stem','καλός','extra')", "unknown, unknown, unknown", "8"},
			{"SELECT 'α' AS prefix, ts_lexize(true,'καλός')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "greek_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('greek_absent','rats')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "greek_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('greek_absent','καλός')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_no_template (TEMPLATE=greek_absent,Language=greek)", "42704", `text search template "greek_absent" does not exist`)
		for _, prepared := range []bool{false, true} {
			d := dictionaryDiagnostic("42602", "invalid name syntax")
			d.Position = "18"
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('a..b','καλός')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY \"greek_α_prefix\" (TEMPLATE=snowball,Language=greek) "
		d := dictionaryDiagnostic("42601", `syntax error at or near "invalid_suffix"`)
		d.Position = fixture.Position(len([]rune(prefix)) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(prefix+"invalid_suffix", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('greek_α_prefix','greek_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func greekSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.greek_owned (TEMPLATE=pg_catalog.snowball,Language=greek,StopWords=english)", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.greek_denied (TEMPLATE=snowball,Language=greek)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.greek_denied (TEMPLATE=snowball,Language=greek)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_missing_schema.denied (TEMPLATE=snowball,Language=greek)", "3F000", `schema "greek_missing_schema" does not exist`)
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.greek_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.greek_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.greek_owned")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.greek_owned','ONLY') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.greek_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'greek', stopwords = 'english'"}})
	})
}
