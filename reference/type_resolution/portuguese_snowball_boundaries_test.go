package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePortugueseSnowballBoundaries(t *testing.T) {
	t.Run("original-regions-and-phase-order", func(t *testing.T) {
		portugueseCorpus(t, [][2]string{
			{"abacico", "abac"}, {"abico", "abic"}, {"baca", "bac"}, {"aba", "aba"},
			{"bacar", "bac"}, {"abcar", "abcar"}, {"aaae", "aaae"}, {"abamente", "ab"},
			{"abaceira", "abaceir"}, {"abacira", "abac"},
			{"abaciando", "abac"}, {"abacgue", "abacg"}, {"abaccie", "abacc"},
			{"ç", "c"}, {"casas!", "casas!"}, {"cafe\u0301", "cafe\u0301"},
			{"abaca~", "abacã"}, {"abaco~", "abacõ"},
			{"ã", "ã"}, {"õ", "õ"}, {"ü", "ü"}, {"Ç", "Ç"},
		})
	})
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"não", "{}"}, {"Não", "{}"}, {"tem", "{}"}, {"tém", "{}"},
			{"você", "{}"}, {"nós", "{}"}, {"às", "{}"},
			{"NÃO", "{nÃo}"}, {"nao", "{nao}"}, {"têm", "{têm}"}, {"sim", "{sim}"},
			{"CASAS", "{cas}"}, {"corações", "{coraçõ}"},
			{"casas!", "{casas!}"}, {"casas ", `{"casas "}`}, {" casas ", `{" casas "}`}, {" ", `{" "}`},
			{strings.Repeat("AB", 498) + "ando", "{" + strings.Repeat("ab", 498) + "}"},
			{strings.Repeat("AB", 498) + "aando", "{" + strings.Repeat("ab", 498) + "aando}"},
			// 498 two-byte á plus four bytes ando is exactly 1000.
			// Extra a makes 1001, despite having only 503 code points.
			{strings.Repeat("á", 498) + "ando", "{" + strings.Repeat("á", 498) + "and}"},
			{strings.Repeat("á", 498) + "aando", "{" + strings.Repeat("á", 498) + "aando}"},
			{"", "{}"},
		} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('portuguese_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('portuguese_stem',NULL::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}}))
	})
}
