package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceYiddishSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, pair := range [][2]string{{"קינדער", "קינד"}, {"RATS", "rats"}, {"ONLY", "only"}, {"געבראַכט", "ברענג"}} {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('yiddish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + pair[1] + "}"}}, pair[0]))
		}
		lithuanianSmokeBoth(t, p, b, c, yiddishDebugStep())
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('yiddish','קינדער RATS 123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':3 'rats':2 'קינד':1"}}))
		for _, q := range []string{
			"CREATE TEXT SEARCH DICTIONARY yiddish_smoke (TEMPLATE=snowball,Language=yiddish,StopWords=english)",
			"CREATE TEXT SEARCH CONFIGURATION yiddish_smoke_config (COPY=yiddish)",
			"ALTER TEXT SEARCH CONFIGURATION yiddish_smoke_config ALTER MAPPING FOR asciiword,word WITH yiddish_smoke",
			"CREATE TABLE yiddish_smoke_carriers(d regdictionary DEFAULT 'yiddish_smoke'::regdictionary,c regconfig DEFAULT 'yiddish_smoke_config'::regconfig)",
		} { regtsExec(t, p, b, c, q, 0) }
		regtsExec(t, p, b, c, "INSERT INTO yiddish_smoke_carriers DEFAULT VALUES", 1)
		const constantSQL = "SELECT ts_lexize('yiddish_smoke','ONLY') AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil { t.Fatal(err) }
		defer constant.Close()
		warm := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		const parameterSQL = "SELECT ts_lexize('yiddish_smoke',$1::text) AS lexemes"
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil { t.Fatal(err) }
		defer parameter.Close()
		live := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		for range 6 { dictionaryResult(t, p, b, warm, constantSQL, "{}") }
		check := func(only, options, vector string) {
			dictionaryResult(t, p, b, live, parameterSQL, only, "ONLY")
			dictionaryResult(t, p, b, c, constantSQL, only)
			lithuanianSmokeBoth(t, p, b, c, fixture.Select(parameterSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{קינד}"}}, "קינדער"))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('yiddish_smoke_config','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
			regtsSelect(t, p, b, c, "SELECT d='yiddish_smoke'::regdictionary AS dictionary_same,c='yiddish_smoke_config'::regconfig AS config_same FROM yiddish_smoke_carriers", []string{"dictionary_same", "config_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		}
		check("{}", "language = 'yiddish', stopwords = 'english'", "'קינד':2")
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "yiddish_smoke", "yiddish_smoke_config")
		yiddishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_smoke (language=english,stopwords=yiddish)")
		preserved()
		check("{}", "language = 'yiddish', stopwords = 'english'", "'קינד':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_smoke (stopwords)", 0)
		check("{only}", "language = 'yiddish'", "'only':1 'קינד':2")
		dictionaryResult(t, p, b, warm, constantSQL, "{}")
		// Close retained statements before Reset/replay changes definitions.
		constant.Close()
		parameter.Close()
		if b.PG {
			if len(emitted) == 0 { t.Fatal("missing actual smoke stream") }
			for _, q := range []string{"DROP TABLE yiddish_smoke_carriers", "DROP TEXT SEARCH CONFIGURATION yiddish_smoke_config", "DROP TEXT SEARCH DICTIONARY yiddish_smoke"} { regtsExec(t, p, b, c, q, 0) }
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal("actual smoke replay", err) }
		} else {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
			emitted = bytes.Clone(out.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','ONLY') AS lexemes", "{only}")
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
		}
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('yiddish_smoke_config','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'קינד':2"}}))
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'yiddish'"}})
		regtsSelect(t, p, b, c, "SELECT d='yiddish_smoke'::regdictionary AS dictionary_same,c='yiddish_smoke_config'::regconfig AS config_same FROM yiddish_smoke_carriers", []string{"dictionary_same", "config_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
	})
}
