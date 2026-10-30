package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEstonianSnowballOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY estonian_options (TEMPLATE=pg_catalog.snowball,Language='EsToNiAn',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION estonian_options_config (COPY=estonian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION estonian_options_config ALTER MAPPING FOR asciiword WITH estonian_options", 0)
		q := "SELECT ts_lexize('estonian_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "THE")
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{rats}", "RATS")
		dictionaryResult(t, p, b, held, q, "{raama}", "raamatud")
		for _, executor := range []fixture.Executor{c, held} {
			dictionaryResult(t, p, b, executor, q, "{viima}", "viin")
			dictionaryResult(t, p, b, executor, q, "{raama}", "raamatud")
			dictionaryResult(t, p, b, executor, q, "{rats}", "RATS")
			dictionaryResult(t, p, b, executor, q, "{}", "ONLY")
			dictionaryResult(t, p, b, executor, q, "{}", "THE")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RATS') AS lexemes", "{rat}")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'EsToNiAn'", "{only}"},
			{"dummy", "language = 'EsToNiAn'", "{only}"},
			{"stopwords=''", "language = 'EsToNiAn', stopwords = ''", "{only}"},
			{"language=estonian,language='EsToNiAn'", "stopwords = '', language = 'EsToNiAn'", "{only}"},
			{"stopwords=english", "language = 'EsToNiAn', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='estonian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "ONLY")
			dictionaryResult(t, p, b, c, q, "{rats}", "RATS")
		}
		dictionaryResult(t, p, b, held, q, "{raama}", "raamatud")
		estonianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_options (language=english,stopwords=estonian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='estonian_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'EsToNiAn', stopwords = 'english'"}})
		dictionaryResult(t, p, b, held, q, "{raama}", "raamatud")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='estonian_options'"
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
			{"stopwords,language=estonian_bogus", "42704", `no Snowball stemmer available for language "estonian_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_options ("+tc.opt+")", tc.code, tc.message)
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{rats}", "RATS")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('estonian_options_config','RATS ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='estonian_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='estonian_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func TestReferenceEstonianSnowballDiagnostics(t *testing.T) {
	t.Run("ownership", estonianSnowballOwnership)
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=estonian,language=estonian", "22023", "multiple Language parameters"},
		{"language=estonian,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=estonian", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=estonian`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=estonian_bogus,language=estonian", "42704", `no Snowball stemmer available for language "estonian_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' estonian '", "42704", `no Snowball stemmer available for language " estonian " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=estonian,stopwords='../estonian'", "22023", `invalid text search configuration file name "../estonian"`},
		{"language=estonian,stopwords='ENGLISH'", "22023", `invalid text search configuration file name "ENGLISH"`},
		{"language=estonian,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
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
				const snapshot = "SELECT oid::bigint,dicttemplate::bigint,dictowner::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='estonian_stem'::regdictionary"
				if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&dict, &template, &owner, &namespace, &initial); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT 'estonian'::regconfig::oid::bigint").Scan(&config); err != nil {
					t.Fatal(err)
				}
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY estonian_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='estonian_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				regtsSelect(t, p, b, c, snapshot, []string{"oid", "dicttemplate", "dictowner", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{dict, template, owner, namespace, initial}})
				regtsSelect(t, p, b, c, "SELECT 'estonian'::regconfig::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{config}})
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='estonian'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
				regtsSelect(t, p, b, c, "SELECT to_tsvector('estonian','ja raamatud mõttele RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ja':1 'mõtt':3 'raama':2 'rats':4"}})
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('estonian_stem','viin') AS lexemes", "{viima}")
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
			s := fixture.ErrorDiagnostic(q, d, "estonian_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=estonian", "language=estonian,stopwords=estonian,stopwords=english"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY estonian_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "estonian.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "estonian.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'raamatud')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('estonian_stem',1)", "unknown, integer", "8"},
			{"SELECT ts_lexize('estonian_stem')", "unknown", "8"},
			{"SELECT ts_lexize('estonian_stem','raamatud','extra')", "unknown, unknown, unknown", "8"},
			{"SELECT 'բ' AS prefix, ts_lexize(true,'raamatud')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "estonian_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('estonian_absent','rats')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "estonian_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('estonian_absent','raamatud')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY estonian_no_template (TEMPLATE=estonian_absent,Language=estonian)", "42704", `text search template "estonian_absent" does not exist`)
		for _, prepared := range []bool{false, true} {
			d := dictionaryDiagnostic("42602", "invalid name syntax")
			d.Position = "18"
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('a..b','raamatud')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY estonian_prefix (TEMPLATE=snowball,Language=estonian) "
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
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('estonian_prefix','estonian_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func estonianSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.estonian_owned (TEMPLATE=pg_catalog.snowball,Language=estonian,StopWords=english)", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.estonian_denied (TEMPLATE=snowball,Language=estonian)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.estonian_denied (TEMPLATE=snowball,Language=estonian)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY estonian_missing_schema.denied (TEMPLATE=snowball,Language=estonian)", "3F000", `schema "estonian_missing_schema" does not exist`)
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.estonian_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.estonian_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.estonian_owned")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.estonian_owned','ONLY') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.estonian_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'estonian', stopwords = 'english'"}})
	})
}
