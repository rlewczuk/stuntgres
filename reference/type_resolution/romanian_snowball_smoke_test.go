package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func romanianMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "romanian.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "romanian.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceRomanianSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"OCOLUL", "ocol"}, {"abrutizanţi", "abrutizanț"}, {"și", "și"}, {"THE", "the"}} {
			romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('romanian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'romanian'"}})
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian','OCOLUL și ocolește') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ocol':1,3 'și':2"}}))
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('romanian','OCOLUL')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "OCOLUL", "{romanian_stem}", "romanian_stem", "{ocol}"}}))
		for _, q := range []string{
			"CREATE TEXT SEARCH DICTIONARY romanian_smoke (TEMPLATE=snowball,Language=romanian,StopWords=english)",
			"CREATE TEXT SEARCH CONFIGURATION romanian_smoke_config (COPY=romanian)",
			"ALTER TEXT SEARCH CONFIGURATION romanian_smoke_config ALTER MAPPING FOR asciiword,word WITH romanian_smoke",
			"CREATE TABLE romanian_smoke_carriers(d regdictionary DEFAULT 'romanian_smoke'::regdictionary,c regconfig DEFAULT 'romanian_smoke_config'::regconfig)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO romanian_smoke_carriers DEFAULT VALUES", 1)
		check := func(the, options, vector string) {
			romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('romanian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{the}}, "THE"))
			romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('romanian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ocol}"}}, "OCOLUL"))
			romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian_smoke_config','THE OCOLUL') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
			regtsSelect(t, p, b, c, "SELECT d='romanian_smoke'::regdictionary AS dictionary_same,c='romanian_smoke_config'::regconfig AS config_same FROM romanian_smoke_carriers", []string{"dictionary_same", "config_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		}
		check("{}", "language = 'romanian', stopwords = 'english'", "'ocol':2")
		preserved := romanianDefinitionSnapshot(t, p, b, c, "romanian_smoke", "romanian_smoke_config")
		romanianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_smoke (language=english,stopwords=romanian)")
		preserved()
		check("{}", "language = 'romanian', stopwords = 'english'", "'ocol':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_smoke (stopwords)", 0)
		check("{the}", "language = 'romanian'", "'ocol':2 'the':1")
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native smoke stream")
			}
			for _, q := range []string{"DROP TABLE romanian_smoke_carriers", "DROP TEXT SEARCH CONFIGURATION romanian_smoke_config", "DROP TEXT SEARCH DICTIONARY romanian_smoke"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native smoke replay", err)
			}
			check("{the}", "language = 'romanian'", "'ocol':2 'the':1")
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('romanian_smoke','OCOLUL') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		romanianLexizeMetadata(t, b, old)
		retainedTypes, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('romanian_stem','THE') AS lexemes", "{the}")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'romanian'"}})
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check("{the}", "language = 'romanian'", "'ocol':2 'the':1")
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "{ocol}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		if len(retainedTypes) != 1 || retainedTypes[0].Name() != "lexemes" || retainedTypes[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("retained descriptor changed", retainedTypes)
		}
	})
}
