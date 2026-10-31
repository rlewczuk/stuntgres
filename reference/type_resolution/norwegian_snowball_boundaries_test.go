package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNorwegianSnowballBoundaries(t *testing.T) {
	t.Run("original-region-and-release-negatives", func(t *testing.T) {
		norwegianCorpus(t, [][2]string{
			{"baca", "bac"}, {"aba", "aba"}, {"aben", "aben"}, {"abene", "aben"},
			{"abert", "abert"}, {"aberte", "abert"}, {"badt", "badt"},
			{"bacgd", "bacgd"}, {"bacgt", "bacgt"}, {"backt", "backt"},
			{"baks", "baks"}, {"bakks", "bakk"}, {"abks", "abk"}, {"abers", "aber"},
			{"bankers", "bank"}, {"tappers", "tapp"}, {"bacêks", "bacêk"},
			{"bacòks", "bacòk"}, {"bacóks", "bacók"}, {"bacôks", "bacôk"},
			{"cd'en", "cd'en"}, {"båtene!", "båtene!"},
		})
	})
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"på", "{}"}, {"På", "{}"}, {"å", "{}"}, {"og", "{}"}, {"ikkje", "{}"}, {"være", "{}"},
			{"PÅ", "{pÅ}"}, {"Å", "{Å}"}, {"VÆRE", "{vÆre}"},
			{"båtene", "{båt}"}, {"BÅTENE", "{bÅten}"},
			{"pa", "{pa}"}, {"pa\u030a", "{pa\u030a}"}, {"ikkes", "{ikk}"},
			{"båtene!", "{båtene!}"}, {"båtene ", `{"båtene "}`}, {" båtene ", `{" båtene "}`}, {" ", `{" "}`},
			{strings.Repeat("A", 997) + "ene", "{" + strings.Repeat("a", 997) + "en}"},
			{strings.Repeat("A", 998) + "ene", "{" + strings.Repeat("a", 998) + "ene}"},
			{strings.Repeat("å", 498) + "bene", "{" + strings.Repeat("å", 498) + "b}"},
			{strings.Repeat("å", 499) + "bene", "{" + strings.Repeat("å", 499) + "bene}"},
			{"", "{}"},
		} {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('norwegian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('norwegian_stem',NULL::text) AS lexemes", nil)
	})
}
