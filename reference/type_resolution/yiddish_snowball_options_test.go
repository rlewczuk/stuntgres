package reference_test

import (
	"database/sql"
	"path"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceYiddishSnowballStopwords(t *testing.T) {
	yiddishCorpus(t, [][2]string{{"ONLY", "only"}, {"THE", "the"}, {"און", "אונ"}, {"אין", "אינ"}, {"דער", "דער"}})
	// Independent literal REL_18_6 English stoplist, not a production resource.
	words := strings.Fields("i me my myself we our ours ourselves you your yours yourself yourselves he him his himself she her hers herself it its itself they them their theirs themselves what which who whom this that these those am is are was were be been being have has had having do does did doing a an the and but if or because as until while of at by for with about against between into through during before after above below to from up down in out on off over under again further then once here there when where why how all any both each few more most other some such no nor not only own same so than too very s t can will just don should now")
	if len(words) != 127 { t.Fatal(len(words)) }
	for start := 0; start < len(words); start += 24 {
		group := words[start:min(start+24, len(words))]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_stops (TEMPLATE=snowball,Language=yiddish,StopWords=english)", 0)
				const q = "SELECT ts_lexize('yiddish_stops',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, word := range group {
					dictionaryResult(t, p, b, c, q, "{}", word)
					dictionaryResult(t, p, b, held, q, "{}", strings.ToUpper(word))
				}
				dictionaryResult(t, p, b, held, q, "{קינד}", "קינדער")
			})
		})
	}
}

func yiddishMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "yiddish.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "yiddish.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceYiddishSnowballOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_options (TEMPLATE=pg_catalog.snowball,Language='YiDdIsH',StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION yiddish_options_config (COPY=yiddish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION yiddish_options_config ALTER MAPPING FOR asciiword,word WITH yiddish_options", 0)
		const q = "SELECT ts_lexize('yiddish_options',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct { options, metadata, only string }{
			{"stopwords", "language = 'YiDdIsH'", "{only}"},
			{"dummy", "language = 'YiDdIsH'", "{only}"},
			{"stopwords=''", "language = 'YiDdIsH', stopwords = ''", "{only}"},
			{"language=yiddish,language='YiDdIsH'", "stopwords = '', language = 'YiDdIsH'", "{only}"},
			{"stopwords=english", "language = 'YiDdIsH', stopwords = 'english'", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_options ("+tc.options+")", 0)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, tc.only, "ONLY")
				dictionaryResult(t, p, b, e, q, "{קינד}", "קינדער")
				dictionaryResult(t, p, b, e, q, "{running}", "RUNNING")
			}
		}
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "yiddish_options", "yiddish_options_config")
		for _, tc := range []struct { options, code, message string }{
			{"language", "22023", "missing Language parameter"},
			{"language=english,stopwords,accept=true", "22023", `unrecognized Snowball parameter: "accept"`},
			{"stopwords,language=yiddish_bogus", "42704", `no Snowball stemmer available for language "yiddish_bogus" and encoding "UTF8"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_options ("+tc.options+")", tc.code, tc.message)
			preserved()
			dictionaryResult(t, p, b, held, q, "{}", "ONLY")
			dictionaryResult(t, p, b, held, q, "{קינד}", "קינדער")
			regtsSelect(t, p, b, c, "SELECT to_tsvector('yiddish_options_config','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'קינד':2"}})
		}
		yiddishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_options (language=english,stopwords=yiddish)")
		preserved()
	})
}

func TestReferenceYiddishSnowballDiagnostics(t *testing.T) {
	cases := []struct { options, code, message string }{
		{"", "22023", "missing Language parameter"},
		{"language=yiddish,language=yiddish", "22023", "multiple Language parameters"},
		{"language=yiddish,stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"},
		{"accept=true,language=yiddish", "22023", `unrecognized Snowball parameter: "accept"`},
		{`"Language"=yiddish`, "22023", `unrecognized Snowball parameter: "Language"`},
		{"language", "42601", "language requires a parameter"},
		{"stopwords", "42601", "stopwords requires a parameter"},
		{"language=yiddish_bogus,language=yiddish", "42704", `no Snowball stemmer available for language "yiddish_bogus" and encoding "UTF8"`},
		{"language='き未知語'", "42704", `no Snowball stemmer available for language "き未知語" and encoding "UTF8"`},
		{"language=''", "42704", `no Snowball stemmer available for language "" and encoding "UTF8"`},
		{"language=' yiddish '", "42704", `no Snowball stemmer available for language " yiddish " and encoding "UTF8"`},
		{"language=0", "42704", `no Snowball stemmer available for language "0" and encoding "UTF8"`},
		{"language=true", "42704", `no Snowball stemmer available for language "true" and encoding "UTF8"`},
		{"language=yiddish,stopwords='../yiddish'", "22023", `invalid text search configuration file name "../yiddish"`},
		{"language=yiddish,stopwords='YIDDISH'", "22023", `invalid text search configuration file name "YIDDISH"`},
		{"language=yiddish,stopwords='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for _, tc := range cases {
		t.Run(tc.options, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" { options = "," + tc.options }
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_bad (TEMPLATE=snowball"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='yiddish_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, options := range []string{"stopwords=yiddish", "language=yiddish,stopwords=yiddish,stopwords=english"} {
			yiddishMissingStopwords(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_missing (TEMPLATE=snowball,"+options+")")
		}
		const q = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		for _, input := range []any{string([]byte{0xff}), []byte{0xff}, "א\x00", []byte{'a', 0}} {
			suffix := "0x00"
			switch v := input.(type) { case string: if len(v) == 1 { suffix = "0xff" }; case []byte: if len(v) == 1 { suffix = "0xff" } }
			d := dictionaryDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+suffix)
			d.Context = "unnamed portal parameter $2"
			s := fixture.ErrorDiagnostic(q, d, "yiddish_stem", input)
			s.ErrorStage, s.SelectRows = "execute", true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		for _, tc := range []struct { q, signature, position string }{
			{"SELECT ts_lexize(true,'קינדער')", "boolean, unknown", "8"},
			{"SELECT ts_lexize('yiddish_stem',1)", "unknown, integer", "8"},
			{"SELECT 'א' AS prefix, ts_lexize(true,'קינדער')", "boolean, unknown", "23"},
			{"SELECT ts_lexize('yiddish_stem')", "unknown", "8"},
		} {
			d := dictionaryDiagnostic("42883", "function ts_lexize("+tc.signature+") does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = tc.position
			yiddishBindingError(t, p, b, c, tc.q, d)
		}
		for _, tc := range []struct { q, message, position string }{
			{"SELECT ts_lexize('yiddish_absent','קינדער')", `text search dictionary "yiddish_absent" does not exist`, "18"},
			{"SELECT to_tsvector('yiddish_absent','קינדער')", `text search configuration "yiddish_absent" does not exist`, "20"},
		} {
			d := dictionaryDiagnostic("42704", tc.message)
			d.Position = tc.position
			yiddishBindingError(t, p, b, c, tc.q, d)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY yiddish_prefix (TEMPLATE=snowball,Language=yiddish) "
		d := dictionaryDiagnostic("42601", `syntax error at or near "invalid_suffix"`)
		d.Position = fixture.Position(len(prefix) + 1)
		yiddishBindingError(t, p, b, c, prefix+"invalid_suffix", d)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('yiddish_prefix','yiddish_missing')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func yiddishBindingError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		if prepared { s.ErrorStage = "prepare" }
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceYiddishSnowballOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_owned AUTHORIZATION dictionary_creator", "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.yiddish_owned (TEMPLATE=pg_catalog.snowball,Language=yiddish,StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION dictionary_owned.yiddish_owned_config (COPY=yiddish)", "ALTER TEXT SEARCH CONFIGURATION dictionary_owned.yiddish_owned_config ALTER MAPPING FOR asciiword,word WITH dictionary_owned.yiddish_owned", "RESET ROLE", "SET ROLE dictionary_peer"} { regtsExec(t, p, b, c, q, 0) }
		defer regtsExec(t, p, b, c, "RESET ROLE", 0)
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "dictionary_owned.yiddish_owned", "dictionary_owned.yiddish_owned_config")
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY dictionary_owned.yiddish_denied (TEMPLATE=snowball,Language=yiddish)", "CREATE TEXT SEARCH DICTIONARY pg_catalog.yiddish_denied (TEMPLATE=snowball,Language=yiddish)"} {
			schema := "dictionary_owned"
			if strings.Contains(q, "pg_catalog") { schema = "pg_catalog" }
			dictionaryError(t, p, b, c, q, "42501", "permission denied for schema "+schema)
			preserved()
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_missing_schema.denied (TEMPLATE=snowball,Language=yiddish)", "3F000", `schema "yiddish_missing_schema" does not exist`)
		for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.yiddish_owned (stopwords)", "DROP TEXT SEARCH DICTIONARY dictionary_owned.yiddish_owned"} {
			dictionaryError(t, p, b, c, q, "42501", "must be owner of text search dictionary dictionary_owned.yiddish_owned")
			preserved()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.yiddish_owned','ONLY') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.yiddish_owned','קינדער') AS lexemes", "{קינד}")
		regtsSelect(t, p, b, c, "SELECT to_tsvector('dictionary_owned.yiddish_owned_config','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'קינד':2"}})
		regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictinitoption FROM pg_ts_dict WHERE oid='dictionary_owned.yiddish_owned'::regdictionary", []string{"owned", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'yiddish', stopwords = 'english'"}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='yiddish_denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
