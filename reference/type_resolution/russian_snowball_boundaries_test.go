package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRussianSnowballBoundaries(t *testing.T) {
	t.Run("original-regions-and-stage-order", func(t *testing.T) {
		russianCorpus(t, [][2]string{
			{"ав", "ав"}, {"абв", "абв"}, {"абав", "аба"}, {"абяв", "абя"},
			{"абив", "аб"}, {"абывшись", "аб"}, {"ала", "ал"}, {"абала", "аба"},
			{"абла", "абл"}, {"абил", "аб"}, {"абилсь", "аб"}, {"абился", "аб"},
			{"абавшая", "аба"}, {"абившая", "аб"}, {"абвшая", "абвш"},
			{"величие", "велич"}, {"кость", "кост"}, {"абабость", "абаб"},
			{"абанн", "абан"}, {"абаннейший", "абан"}, {"абаннейше", "абан"},
			{"абейш", "аб"}, {"абь", "аб"}, {"абан", "аба"},
			{"машины", "машин"}, {"МАШИНЫ", "МАШИНЫ"}, {"Машины", "Машин"},
			{"ё", "е"}, {"Ё", "Ё"}, {"ёлки", "елк"}, {"е\u0308лки", "е\u0308лк"},
			{"машины\u0301", "машины\u0301"}, {"машины!", "машины!"},
			{"Xмашины", "xмашин"},
		})
	})
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('russian_stem',$1::text) AS lexemes"
		stem := strings.Repeat("я", 498)
		for _, tc := range []struct {
			input any
			want  any
		}{
			{"машины ", `{"машины "}`}, {"", "{}"}, {nil, nil},
			{stem + "ам", "{" + stem + "}"},
			{stem + "aам", "{" + stem + "aам}"},
			{stem + "ами", "{" + stem + "ами}"},
			{strings.Repeat("я", 497) + "ёAам", "{" + strings.Repeat("я", 497) + "ёaам}"},
		} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input))
		}
	})
}
