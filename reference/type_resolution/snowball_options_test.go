package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnowballOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY snowball_options (TEMPLATE=pg_catalog.snowball,Language='EnGlIsH',StopWords=english)", 0)
		q := "SELECT ts_lexize('snowball_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'EnGlIsH'", "{onli}"},
			{"dummy", "language = 'EnGlIsH'", "{onli}"},
			{"stopwords=''", "language = 'EnGlIsH', stopwords = ''", "{onli}"},
			{"language=english,language='EnGlIsH'", "stopwords = '', language = 'EnGlIsH'", "{onli}"},
			{"stopwords=english", "language = 'EnGlIsH', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY snowball_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='snowball_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			dictionaryResult(t, p, b, c, q, "{rat}", "RATS")
		}
		var id, owner, template int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='snowball_options'"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &options); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ opt, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"stopwords,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=snowball_bogus", "42704", `no Snowball stemmer available for language "snowball_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY snowball_options ("+tc.opt+")", tc.code, tc.message)
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{rat}", "RATS")
		}
	})
}

func TestReferenceSnowballDiagnostics(t *testing.T) {
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=english,language=english", "22023", "multiple Language parameters"},
		{"language=english,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=english", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=english`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=snowball_bogus,language=english", "42704", `no Snowball stemmer available for language "snowball_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' english '", "42704", `no Snowball stemmer available for language " english " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=english,stopwords='../english'", "22023", `invalid text search configuration file name "../english"`},
		{"language=english,stopwords='ENGLISH'", "22023", `invalid text search configuration file name "ENGLISH"`},
		{"language=english,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for i, tc := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY snowball_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='snowball_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, options := range []string{"stopwords=snowball_missing", "language=english,stopwords=snowball_missing,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY snowball_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "snowball_missing.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "/usr/share/postgresql/18/tsearch_data/snowball_missing.stop": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'rats')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('english_stem',1)", "unknown, integer", "8"},
			{"SELECT 'é' AS prefix, ts_lexize(true,'rats')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "snowball_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('snowball_absent','rats')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY snowball_prefix (TEMPLATE=snowball,Language=english) "
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
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('snowball_prefix','snowball_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.snowball_owned (TEMPLATE=pg_catalog.snowball,Language=english,StopWords=english)", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.snowball_denied (TEMPLATE=snowball,Language=english)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.snowball_denied (TEMPLATE=snowball,Language=english)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY snowball_missing_schema.denied (TEMPLATE=snowball,Language=english)", "3F000", `schema "snowball_missing_schema" does not exist`)
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.snowball_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.snowball_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.snowball_owned")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.snowball_owned','ONLY') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.snowball_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'english', stopwords = 'english'"}})
	})
}
