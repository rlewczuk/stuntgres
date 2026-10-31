package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceNepaliSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"किताबहरूलाई", "{किताब}"}, {"घरत्रै", "{घरत्र}"}, {"घरहरूनु", "{घर}"}, {"सबै", "{}"}, {"RATS", "{rats}"}, {"THE", "{the}"}} {
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('nepali_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		nepaliSmokeBoth(t, p, b, c, nepaliDebugStep())
		nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('nepali','RATS र घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1 'घर':3"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY nepali_smoke (TEMPLATE=snowball,Language='NePaLi',StopWords=nepali)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION nepali_smoke_config (COPY=nepali)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_smoke_config ALTER MAPPING FOR asciiword,word WITH nepali_smoke", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_smoke (stopwords)", 0)
		check := func() {
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('nepali_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{सबै}"}}, "सबै"))
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('nepali_smoke_config','सबै घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'घर':2 'सबै':1"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='nepali_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'NePaLi'"}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('nepali_smoke','घरहरू') AS lexemes", "{घर}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('nepali_stem','सबै') AS lexemes", "{}")
		}
		check()
		preserved := nepaliDefinitionSnapshot(t, p, b, c, "nepali_smoke", "nepali_smoke_config")
		nepaliMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_smoke (language=english,stopwords=nepali_missing)")
		preserved()
		check()
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
