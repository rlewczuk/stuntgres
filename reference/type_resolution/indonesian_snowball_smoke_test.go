package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIndonesianSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"bukulah!", "{bukulah!}"}, {"MENYAPU", "{sapu}"}, {"MÉNYAPU", "{mÉnyapu}"},
			{"áákan", "{áákan}"}, {"áákan", "{áá}"},
			{strings.Repeat("a", 997)+"kan", "{"+strings.Repeat("a", 997)+"}"},
			{strings.Repeat("A", 998)+"kan", "{"+strings.Repeat("a", 998)+"kan}"},
			{"á"+strings.Repeat("a", 995)+"kan", "{á"+strings.Repeat("a", 995)+"}"},
			{"á"+strings.Repeat("A", 996)+"kan", "{á"+strings.Repeat("a", 996)+"kan}"},
		} {
			indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
	})
}

func indonesianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('indonesian','MENYAPU dan')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "MENYAPU", "{indonesian_stem}", "indonesian_stem", "{sapu}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"asciiword", "Word, all ASCII", "dan", "{indonesian_stem}", "indonesian_stem", "{dan}"},
	})
}

func TestReferenceIndonesianSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"MENYAPU", "{sapu}"}, {"pelajaran", "{ajar}"}, {"perbaikan", "{baik}"}, {"televisi", "{televisi}"}, {"dan", "{dan}"}, {"THE", "{the}"}, {"RATS", "{rats}"}} {
			indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		indonesianSmokeBoth(t, p, b, c, indonesianDebugStep())
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('indonesian','Pelajaran menyapu buku dan televisi') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ajar':1 'buku':3 'dan':4 'sapu':2 'televisi':5"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY indonesian_smoke (TEMPLATE=snowball,Language=indonesian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION indonesian_smoke_config (COPY=indonesian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION indonesian_smoke_config ALTER MAPPING FOR asciiword,word WITH indonesian_smoke", 0)
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_smoke','THE') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{}"}}))
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_smoke','MENYAPU') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{sapu}"}}))
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_smoke (stopwords)", 0)
		check := func() {
			for _, tc := range [][2]string{{"THE", "{the}"}, {"MENYAPU", "{sapu}"}, {"RATS", "{rats}"}} {
				indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('indonesian_smoke_config','THE MENYAPU') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'sapu':2 'the':1"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='indonesian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'indonesian'"}})
		}
		check()
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'indonesian_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil || identity <= 0 {
			t.Fatal(identity, err)
		}
		indonesianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_smoke (language=english,stopwords=indonesian)")
		regtsSelect(t, p, b, c, "SELECT 'indonesian_smoke'::regdictionary::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{identity}})
		check()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native stream required; PG-only discovery cannot prove replay")
			}
			regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION indonesian_smoke_config", 0)
			regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY indonesian_smoke", 0)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted smoke replay", err)
			}
			check()
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
	})
}
