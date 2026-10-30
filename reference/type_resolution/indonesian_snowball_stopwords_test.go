package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIndonesianSnowballStopwords(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// REL_18_6 ships no indonesian.stop; these are not inferred stopwords.
		for _, tc := range [][2]string{{"dan", "dan"}, {"yang", "yang"}, {"dengan", "dengan"}, {"THE", "the"}, {"adalah", "ada"}} {
			indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{"+tc[1]+"}"}}, tc[0]))
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY indonesian_stops (TEMPLATE=snowball,Language=indonesian,StopWords=english)", 0)
		for _, tc := range [][2]string{{"THE", "{}"}, {"ONLY", "{}"}, {"MENYAPU", "{sapu}"}, {"dan", "{dan}"}, {"RATS", "{rats}"}} {
			indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('indonesian_stops',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		indonesianMissingStopwords(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY indonesian_absent_list (TEMPLATE=snowball,Language=indonesian,StopWords=indonesian)")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='indonesian_absent_list'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
