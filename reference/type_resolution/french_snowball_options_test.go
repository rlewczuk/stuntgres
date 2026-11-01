package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFrenchSnowballOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY french_options (TEMPLATE=pg_catalog.snowball,Language='FrEnCh',StopWords=french)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION french_options_config (COPY=french)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION french_options_config ALTER MAPPING FOR asciiword WITH french_options", 0)
		q := "SELECT ts_lexize('french_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "été")
		dictionaryResult(t, p, b, held, q, "{}", "été")
		dictionaryResult(t, p, b, held, q, "{chat}", "CHATS")
		dictionaryResult(t, p, b, held, q, "{cheval}", "chevaux")
		for _, executor := range []fixture.Executor{c, held} {
			dictionaryResult(t, p, b, executor, q, "{cheval}", "chevaux")
			dictionaryResult(t, p, b, executor, q, "{cheval}", "chevaux")
			dictionaryResult(t, p, b, executor, q, "{chat}", "CHATS")
			dictionaryResult(t, p, b, executor, q, "{}", "été")
			dictionaryResult(t, p, b, executor, q, "{}", "été")
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','CHATS') AS lexemes", "{chat}")
		for _, tc := range []struct{ options, metadata, only string }{
			{"stopwords", "language = 'FrEnCh'", "{été}"},
			{"dummy", "language = 'FrEnCh'", "{été}"},
			{"stopwords=''", "language = 'FrEnCh', stopwords = ''", "{été}"},
			{"language=french,language='FrEnCh'", "stopwords = '', language = 'FrEnCh'", "{été}"},
			{"stopwords=french", "language = 'FrEnCh', stopwords = 'french'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='french_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			dictionaryResult(t, p, b, held, q, tc.only, "été")
			dictionaryResult(t, p, b, c, q, "{chat}", "CHATS")
		}
		dictionaryResult(t, p, b, held, q, "{cheval}", "chevaux")
		frenchMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_options (language=english,stopwords=french_missing)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='french_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'FrEnCh', stopwords = 'french'"}})
		dictionaryResult(t, p, b, held, q, "{cheval}", "chevaux")
		var id, owner, template, namespace int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='french_options'"
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
			{"stopwords,language=french_bogus", "42704", `no Snowball stemmer available for language "french_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_options ("+tc.opt+")", tc.code, tc.message)
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			dictionaryResult(t, p, b, held, q, "{}", "été")
			dictionaryResult(t, p, b, held, q, "{chat}", "CHATS")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('french_options_config','CHATS été') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'chat':1"}})
			regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='french_options'::regdictionary::oid AS current FROM pg_ts_config_map WHERE mapcfg='french_options_config'::regconfig AND maptokentype=1", []string{"mapseqno", "current"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed options changed whole catalog", err)
				}
			}
		}
	})
}

func TestReferenceFrenchSnowballDiagnostics(t *testing.T) {
	t.Run("ownership", frenchSnowballOwnership)
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=french,language=french", "22023", "multiple Language parameters"},
		{"language=french,stopwords=french,stopwords=french", "22023", "multiple StopWords parameters"},
		{"accept=true,language=french", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=french`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=french_bogus,language=french", "42704", `no Snowball stemmer available for language "french_bogus" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' french '", "42704", `no Snowball stemmer available for language " french " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=french,stopwords='../french'", "22023", `invalid text search configuration file name "../french"`},
		{"language=french,stopwords='FRENCH'", "22023", `invalid text search configuration file name "FRENCH"`},
		{"language=french,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
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
				const snapshot = "SELECT oid::bigint,dicttemplate::bigint,dictowner::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='french_stem'::regdictionary"
				if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&dict, &template, &owner, &namespace, &initial); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT 'french'::regconfig::oid::bigint").Scan(&config); err != nil {
					t.Fatal(err)
				}
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY french_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='french_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				regtsSelect(t, p, b, c, snapshot, []string{"oid", "dicttemplate", "dictowner", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{dict, template, owner, namespace, initial}})
				regtsSelect(t, p, b, c, "SELECT 'french'::regconfig::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{config}})
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='french'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
				regtsSelect(t, p, b, c, "SELECT to_tsvector('french','aux chevaux et aux hôtels') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cheval':2 'hôtel':5"}})
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('french_stem','chevaux') AS lexemes", "{cheval}")
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
		for _, input := range []any{string([]byte{0xff}), []byte{0xff}, "é\x00", []byte{'a', 0}} {
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
			s := fixture.ErrorDiagnostic(q, d, "french_stem", input)
			s.ErrorStage = "execute"
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, options := range []string{"stopwords=french_missing", "language=french,stopwords=french_missing,stopwords=french"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY french_missing_data (TEMPLATE=snowball,"+options+")", dictionaryDiagnostic("F0000", `could not open stop-word file "french_missing.stop": No such file or directory`))
				pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "french_missing.stop")+`": No such file or directory`)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize(true,'chevaux')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('french_stem',1)", "unknown, integer", "8"},
			{"SELECT ts_lexize('french_stem')", "unknown", "8"},
			{"SELECT ts_lexize('french_stem','chevaux','extra')", "unknown, unknown, unknown", "8"},
			{"SELECT 'é' AS prefix, ts_lexize(true,'chevaux')", "boolean, unknown", "23"},
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
		missing := dictionaryDiagnostic("42704", `text search dictionary "french_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('french_absent','chats')", missing)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		missingConfig := dictionaryDiagnostic("42704", `text search configuration "french_absent" does not exist`)
		missingConfig.Position = "20"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT to_tsvector('french_absent','chevaux')", missingConfig)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY french_no_template (TEMPLATE=french_absent,Language=french)", "42704", `text search template "french_absent" does not exist`)
		for _, prepared := range []bool{false, true} {
			d := dictionaryDiagnostic("42602", "invalid name syntax")
			d.Position = "18"
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('a..b','chevaux')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY french_prefix (TEMPLATE=snowball,Language=french) "
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
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('french_prefix','french_missing_data')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func frenchSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.french_owned (TEMPLATE=pg_catalog.snowball,Language=french,StopWords=french)", "RESET ROLE", "SET ROLE dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		var id, owner, template, namespace int64
		var options string
		const snapshot = "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictnamespace::bigint,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.french_owned'::regdictionary"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &namespace, &options); err != nil {
			t.Fatal(err)
		}
		q := "SELECT ts_lexize('dictionary_owned.french_owned',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		preserved := func() {
			regtsSelect(t, p, b, c, snapshot, []string{"oid", "dictowner", "dicttemplate", "dictnamespace", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, namespace, options}})
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, "{cheval}", "CHEVAUX")
				dictionaryResult(t, p, b, e, q, "{}", "été")
			}
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='french_denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("authorization failure changed whole destination", err)
				}
			}
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.french_denied (TEMPLATE=snowball,Language=french)", "42501", "permission denied for schema dictionary_owned")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.french_denied (TEMPLATE=snowball,Language=french)", "42501", "permission denied for schema pg_catalog")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY french_missing_schema.denied (TEMPLATE=snowball,Language=french)", "3F000", `schema "french_missing_schema" does not exist`)
		preserved()
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.french_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.french_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.french_owned")
			preserved()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.french_owned','été') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.french_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'french', stopwords = 'french'"}})
	})
}
