package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTurkishSnowballBoundaries(t *testing.T) {
	t.Run("released-apostrophe-and-unicode", func(t *testing.T) {
		turkishCorpus(t, [][2]string{
			{"kitap'lar", "kitap"}, {"Türkiye'dir", "türki"},
			{"o'connor", "o"}, {"a'lar", "a"}, {"'kitaplar", "'kitaplar"},
			{"kitap'lar'da", "kitap"}, {"kitaplar", "kitap"},
			{"kitaplar’", "kitaplar’"}, {"’kitaplar", "’kitap"},
			{"öböxler", "öböx"}, {"o\u0308bo\u0308xler", "o\u0308bo\u0308xler"},
			{"kitaplar!", "kitaplar!"},
		})
	})
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"KITAPLAR", "{kitap}"}, {"KİTAPLAR", "{kİtap}"},
			{"I", "{i}"}, {"İ", "{İ}"}, {"ı", "{ı}"},
			{"BIZ", "{}"}, {"BİZ", "{bİz}"}, {"çok", "{}"}, {"ÇOK", "{Çok}"},
			{"Mı", "{}"}, {"MI", "{mi}"}, {"kitaplar!", "{kitaplar!}"},
			{"kitaplar ", `{"kitaplar "}`}, {" ", `{" "}`},
			{strings.Repeat("AB", 498) + "alar", "{" + strings.Repeat("ab", 498) + "a}"},
			{strings.Repeat("AB", 498) + "aalar", "{" + strings.Repeat("ab", 498) + "aalar}"},
			{strings.Repeat("ö", 498) + "eler", "{" + strings.Repeat("ö", 498) + "e}"},
			{strings.Repeat("ö", 498) + "eeler", "{" + strings.Repeat("ö", 498) + "eeler}"},
			// Original 1001-byte guard precedes apostrophe truncation.
			{"A'" + strings.Repeat("B", 999), "{a'" + strings.Repeat("b", 999) + "}"},
			{"", "{}"},
		} {
			turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('turkish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('turkish_stem',NULL::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}}))
	})
}
