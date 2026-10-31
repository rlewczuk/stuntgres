package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Fixed expectations trace REL_18_6 stem_UTF_8_norwegian.c, not modern
// Snowball 3.x rules or native provider tables. Real words additionally use
// snowball-data 4f982ec072a091771b321ab7861841471942e14a norwegian/{voc,output}.txt.
func TestReferenceNorwegianSnowballMorphology(t *testing.T) {
	t.Run("historical-vocabulary", func(t *testing.T) {
		norwegianCorpus(t, [][2]string{
			{"aabakken", "aabakk"}, {"absorberes", "absorber"},
			{"adkomstdokumenter", "adkomstdokument"}, {"administrative", "administrativ"},
			{"administrativt", "administrativ"}, {"administrert", "administrer"},
			{"adopsjonspenger", "adopsjonspeng"}, {"adresse", "adress"}, {"adresser", "adress"},
			{"advokatfullmektig", "advokatfullmekt"}, {"akkumulerte", "akkumuler"},
			{"aksjelov", "aksj"}, {"aksjelova", "aksj"}, {"aksjeloven", "aksj"},
			{"aksjelovens", "aksj"}, {"aksjerettslig", "aksjeretts"},
			{"aksjeselskapsrettslege", "aksjeselskapsretts"},
		})
	})
	t.Run("main-suffixes", func(t *testing.T) {
		var cases [][2]string
		for _, suffix := range strings.Fields("a e ede ande ende ane ene hetene en heten ar er heter as es edes endes enes hetenes ens hetens ers ets et het ast") {
			cases = append(cases, [2]string{"bac" + suffix, "bac"})
		}
		cases = append(cases, [2]string{"bacert", "bacer"}, [2]string{"bacerte", "bacer"})
		norwegianCorpus(t, cases)
	})
	t.Run("pairs-other-suffixes-and-stage-order", func(t *testing.T) {
		// Main s deletion preempts els; ene exposes els to the later phase.
		cases := [][2]string{{"bacdt", "bacd"}, {"bacvt", "bacv"}, {"bacelighetene", "bac"}, {"bacdtene", "bacd"}, {"baclegleg", "bacleg"}, {"bacels", "bacel"}, {"bacelsene", "bac"}}
		for _, suffix := range strings.Fields("leg eleg ig eig lig elig lov elov slov hetslov") {
			cases = append(cases, [2]string{"bac" + suffix, "bac"})
		}
		norwegianCorpus(t, cases)
	})
	t.Run("conditional-s", func(t *testing.T) {
		var cases [][2]string
		for _, predecessor := range "bcdfghjlmnoprtvyz" {
			prefix := "bac" + string(predecessor)
			cases = append(cases, [2]string{prefix + "s", prefix})
		}
		for _, predecessor := range "iuæåøqwxs" {
			input := "bac" + string(predecessor) + "s"
			cases = append(cases, [2]string{input, input})
		}
		norwegianCorpus(t, cases)
	})
}

func norwegianCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY norwegian_corpus (TEMPLATE=snowball,Language=norwegian)", 0)
		const query = "SELECT ts_lexize('norwegian_corpus',$1::text) AS lexemes"
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
