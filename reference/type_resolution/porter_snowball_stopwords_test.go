package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePorterSnowballStopwords(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterSetup(t, p, b, c, "porter_stops", "", true)
		for _, state := range []struct{ alter, only, the, s string }{
			{"", "{}", "{}", "{}"},
			{"stopwords", "{onli}", "{the}", "{s}"},
			{"stopwords=''", "{onli}", "{the}", "{s}"},
			{"stopwords=english", "{}", "{}", "{}"},
		} {
			if state.alter != "" {
				regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_stops ("+state.alter+")", 0)
			}
			for _, tc := range [][2]string{{"ONLY", state.only}, {"THE", state.the}, {"s", state.s}, {"SKIES", "{ski}"}, {"", "{}"}} {
				porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('porter_stops',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
		}
	})
}
