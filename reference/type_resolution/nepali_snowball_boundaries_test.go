package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNepaliSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"RATS", "{rats}"}, {"THE", "{the}"}, {"CAFÉS", "{cafÉs}"}, {"café", "{café}"},
			{"CAFE\u0301S", "{cafe\u0301s}"}, {"घरकाँ", "{घरकाँ}"}, {"घरत्रैँ", "{घरत्रैँ}"},
			{"घरको!", "{घरको!}"}, {"घरको ", `{"घरको "}`}, {" घरको ", `{" घरको "}`},
			{"घरत्रै", "{घरत्र}"}, {"त्रै", "{त्र}"}, {"घरकं", "{घरकं}"}, {"घरकँ", "{घरकँ}"},
			{"नु", "{नु}"}, {"हरू", "{हरू}"}, {"", "{}"},
			{strings.Repeat("A", 994) + "को", "{" + strings.Repeat("a", 994) + "}"},
			{strings.Repeat("A", 995) + "को", "{" + strings.Repeat("a", 995) + "को}"},
			{"A" + strings.Repeat("घ", 331) + "को", "{a" + strings.Repeat("घ", 331) + "}"},
			{"AA" + strings.Repeat("घ", 331) + "को", "{aa" + strings.Repeat("घ", 331) + "को}"},
		} {
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('nepali_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('nepali_stem',NULL::text) AS lexemes", nil)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY nepali_boundary_free (TEMPLATE=snowball,Language=nepali)", 0)
		for _, word := range []string{"सबै", "ले"} {
			nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('nepali_boundary_free',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + word + "}"}}, word))
		}
	})
}
