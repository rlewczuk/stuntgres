package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations independently transcribe REL_18_6 stem_UTF_8_swedish.c and
// Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/swedish.sbl.
// Historical pairs come from snowball-data
// 4f982ec072a091771b321ab7861841471942e14a swedish/{voc,output}.txt.
func TestReferenceSwedishSnowballMorphology(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		swedishCorpus(t, [][2]string{
			{"abborrar", "abborr"}, {"abborrarna", "abborr"}, {"abborre", "abborr"},
			{"abborrpinne", "abborrpin"}, {"abrahams", "abraham"}, {"ackompanjerade", "ackompanjer"},
			{"adelig", "ade"}, {"adelsfröken", "adelsfrök"}, {"adelskalendern", "adelskalend"},
			{"adelskalendrar", "adelskalendr"}, {"adelskapen", "adelskap"}, {"adelsmän", "adelsmän"},
			{"adelsvapen", "adelsvap"}, {"adertonhundratalet", "adertonhundratalet"},
			{"adjunkt", "adjunk"}, {"adjunkten", "adjunk"}, {"adjö", "adjö"}, {"adlades", "adl"},
			{"adlig", "adl"}, {"adligt", "adl"}, {"adopterade", "adopter"}, {"adress", "adress"},
			{"adressen", "adress"}, {"adresserat", "adresser"},
		})
	})
	t.Run("main-suffixes", func(t *testing.T) {
		var cases [][2]string
		for _, suffix := range strings.Fields("a arna erna heterna orna ad e ade ande arne are aste en anden aren heten ern ar er heter or as arnas ernas ornas es ades andes ens arens hetens erns at andet het ast") {
			cases = append(cases, [2]string{"bac" + suffix, "bac"})
		}
		swedishCorpus(t, cases)
	})
	t.Run("consonant-pairs", func(t *testing.T) {
		var cases [][2]string
		for _, pair := range []string{"dd", "gd", "nn", "dt", "gt", "kt", "tt"} {
			cases = append(cases, [2]string{"bac" + pair, "bac" + pair[:1]})
		}
		swedishCorpus(t, cases)
	})
	t.Run("conditional-s", func(t *testing.T) {
		var cases [][2]string
		for _, predecessor := range "bcdfghjklmnoprtvy" {
			prefix := "bac" + string(predecessor)
			cases = append(cases, [2]string{prefix + "s", prefix})
		}
		for _, predecessor := range "iuäåöqwxzsé" {
			input := "bac" + string(predecessor) + "s"
			cases = append(cases, [2]string{input, input})
		}
		swedishCorpus(t, cases)
	})
	t.Run("conditional-öst", func(t *testing.T) {
		var cases [][2]string
		for _, predecessor := range "iklnprtuv" {
			prefix := "bac" + string(predecessor)
			cases = append(cases, [2]string{prefix + "öst", prefix + "ös"})
		}
		cases = append(cases, [2]string{"bacböst", "bacböst"})
		swedishCorpus(t, cases)
	})
}

func swedishCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY swedish_corpus (TEMPLATE=snowball,Language=swedish)", 0)
		const query = "SELECT ts_lexize('swedish_corpus',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
					dictionaryResult(t, p, b, e, query, "{"+tc[1]+"}", tc[0])
				}
			})
		}
	})
}
