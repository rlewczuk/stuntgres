package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSwedishSnowballBoundaries(t *testing.T) {
	t.Run("original-region-and-phase-order", func(t *testing.T) {
		swedishCorpus(t, [][2]string{
			{"hundar", "hund"}, {"hunden", "hund"}, {"hundens", "hund"}, {"katterna", "katt"},
			{"flickorna", "flick"}, {"snabbaste", "snabb"},
			{"a", "a"}, {"as", "as"}, {"abs", "abs"}, {"hus", "hus"}, {"ben", "ben"}, {"bba", "bba"},
			{"balig", "bal"}, {"heterna", "het"}, {"åba", "åba"}, {"åbade", "åbad"}, {"åbcade", "åbc"},
			{"babels", "babel"}, {"babelsen", "bab"}, {"babdden", "babd"}, {"babliglig", "bablig"},
			{"babig", "bab"}, {"bablig", "bab"}, {"babfullt", "babfull"}, {"meningslöst", "meningslös"},
			{"babiöst", "babiös"}, {"babböst", "babböst"}, {"babbb", "babbb"}, {"babb", "babb"},
			{"hundarna!", "hundarna!"}, {"hundarna\u0301", "hundarna\u0301"},
			{"café", "café"}, {"cafés", "cafés"}, {"RUNNING", "running"},
			{"HUNDAR", "hund"}, {"HäSTAR", "häst"}, {"HÄSTAR", "hÄstar"}, {"CAFÉS", "cafÉs"}, {"PÅ", "pÅ"},
		})
	})
	t.Run("original-byte-guard", func(t *testing.T) {
		swedishCorpus(t, [][2]string{
			{"bab" + strings.Repeat("a", 995) + "ar", "bab" + strings.Repeat("a", 995)},
			{"BAB" + strings.Repeat("A", 996) + "ar", "bab" + strings.Repeat("a", 996) + "ar"},
			{"bab" + strings.Repeat("å", 497) + "a" + "ar", "bab" + strings.Repeat("å", 497) + "a"},
			{"BAB" + strings.Repeat("å", 498) + "ar", "bab" + strings.Repeat("å", 498) + "ar"},
		})
	})
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"på", "{}"}, {"På", "{}"}, {"PÅ", "{pÅ}"}, {"sitta", "{}"}, {"sitt", "{sitt}"},
			{"hundarna!", "{hundarna!}"}, {"hundarna ", `{"hundarna "}`}, {" hundarna ", `{" hundarna "}`}, {" ", `{" "}`}, {"", "{}"},
		} {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('swedish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		for _, q := range []string{"SELECT ts_lexize('swedish_stem',NULL::text) AS lexemes", "SELECT ts_lexize(NULL::regdictionary,'hundar') AS lexemes"} {
			norwegianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}}))
		}
	})
}
