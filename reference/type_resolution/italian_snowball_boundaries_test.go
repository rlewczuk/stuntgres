package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceItalianSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Source-traced release expectations, not modern Snowball elision rules.
		for _, tc := range [][2]string{
			{"cani", "{can}"}, {"CANI", "{can}"}, {"perché", "{}"}, {"PERCHé", "{}"},
			{"PERCHÉ", "{perchÉ}"}, {"più", "{}"}, {"piú", "{più}"},
			{"perche", "{perc}"}, {"perchè", "{perc}"}, {"CAFfÈ", "{caffÈ}"}, {"caffè", "{caff}"},
			{"ico", "{ico}"}, {"ica", "{ica}"}, {"amo", "{amo}"},
			{"abacar", "{abac}"}, {"abacir", "{abac}"}, {"abacer", "{abacer}"},
			{"bach", "{bach}"}, {"bacch", "{bacc}"}, {"darlo", "{darl}"}, {"abacarlo", "{abac}"}, {"arci", "{arci}"},
			// dell'amico loses ic in R2, not through apostrophe/elision removal.
			{"l'amico", "{l'amic}"}, {"amico", "{amic}"}, {"dell'amico", "{dell'am}"},
			{"divano", "{divan}"}, {"divani", "{divan}"}, {"divanone", "{divanon}"},
			{"cani!", "{cani!}"}, {"cani ", `{"cani "}`}, {" cani ", `{" cani "}`},
			{"caffe\u0300", "{caffe\u0300}"}, {"perche\u0301", "{perche\u0301}"},
			{"abacá", "{abac}"}, {"abacé", "{abac}"}, {"abací", "{abac}"}, {"abacó", "{abac}"}, {"abacú", "{abacù}"},
			{"quadro", "{quadr}"}, {"aiuto", "{aiut}"}, {"abacaia", "{abacai}"}, {"abacaua", "{abacau}"},
			{"abacquiamo", "{abacqu}"}, {"abacYamo", "{abacyam}"},
			{"A" + strings.Repeat("A", 995) + "cani", "{" + strings.Repeat("a", 996) + "can}"},
			{strings.Repeat("A", 997) + "cani", "{" + strings.Repeat("a", 997) + "cani}"},
			{strings.Repeat("à", 498) + "cani", "{" + strings.Repeat("à", 498) + "can}"},
			{strings.Repeat("à", 499) + "cani", "{" + strings.Repeat("à", 499) + "cani}"},
			{strings.Repeat("A", 997) + "abacá", "{" + strings.Repeat("a", 997) + "abacá}"},
			{"", "{}"},
		} {
			italianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('italian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('italian_stem',NULL::text) AS lexemes", nil)
	})
}
