package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSpanishSnowballBoundaries(t *testing.T) {
	t.Run("original-regions-and-phase-order", func(t *testing.T) {
		spanishCorpus(t, [][2]string{
			{"educacion", "educacion"}, {"revolucion", "revolucion"},
			{"abacacion", "abacacion"}, {"abacucion", "abacucion"},
			{"educación", "educ"}, {"revolución", "revolu"},
			{"abacico", "abac"}, {"abico", "abic"}, {"bacar", "bac"},
			{"abcar", "abcar"}, {"aaae", "aaae"}, {"abamente", "ab"},
			{"abacativamente", "abac"}, {"abacosamente", "abac"},
			{"abacicamente", "abac"}, {"abacadamente", "abac"},
			{"abacantemente", "abac"}, {"abacablemente", "abac"},
			{"abaciblemente", "abac"}, {"abacabilidades", "abac"},
			{"abacicidad", "abac"}, {"abacividad", "abac"},
			{"abacativo", "abac"}, {"abacatividades", "abacat"},
			{"abacándome", "abac"}, {"abaciéndolo", "abac"},
			{"abacárselas", "abac"}, {"abacérselo", "abac"}, {"abacírselo", "abac"},
			{"abacuyendolo", "abacu"}, {"abacyendolo", "abacyendol"},
			{"abuyan", "abu"}, {"abacyendo", "abacyend"}, {"agues", "ag"}, {"ague", "agu"},
			{"ü", "ü"}, {"ñ", "ñ"}, {"á", "a"}, {"ÁÉÍÓÚ", "ÁÉÍÓÚ"},
			{"cafe\u0301", "cafe\u0301"}, {"casas!", "casas!"},
			{"más", "mas"}, {"teniendo", "ten"}, {"sintiendo", "sint"}, {"sentido", "sent"},
		})
	})
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"de", "{}"}, {"DE", "{}"}, {"más", "{}"}, {"Más", "{}"}, {"sí", "{}"},
			{"él", "{}"}, {"tú", "{}"}, {"teniendo", "{}"}, {"sintiendo", "{}"},
			{"vosostros", "{}"}, {"vosostras", "{}"}, {"si", "{si}"},
			{"MÁS", "{mÁs}"}, {"Él", "{Él}"}, {"TÚ", "{tÚ}"},
			{"vosotros", "{vosotr}"}, {"vosotras", "{vosotr}"},
			{"CASAS", "{cas}"}, {"NIÑOS", "{niÑ}"},
			{"casas!", "{casas!}"}, {"casas ", `{"casas "}`}, {" casas ", `{" casas "}`}, {" ", `{" "}`},
			{strings.Repeat("AB", 498) + "ando", "{" + strings.Repeat("ab", 498) + "}"},
			{strings.Repeat("AB", 498) + "aando", "{" + strings.Repeat("ab", 498) + "aando}"},
			{strings.Repeat("á", 498) + "ando", "{" + strings.Repeat("a", 498) + "and}"},
			{strings.Repeat("á", 498) + "aando", "{" + strings.Repeat("á", 498) + "aando}"},
			{"", "{}"},
		} {
			spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('spanish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('spanish_stem',NULL::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{nil}}))
	})
}
