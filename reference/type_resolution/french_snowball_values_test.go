package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func frenchCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	for start := 0; start < len(pairs); start += 24 {
		end := start + 24
		if end > len(pairs) {
			end = len(pairs)
		}
		group := pairs[start:end]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY french_rules (TEMPLATE=pg_catalog.snowball,Language='FrEnCh')", 0)
				for _, name := range []string{"french_rules", "pg_catalog.french_stem"} {
					q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, tc := range group {
						want := "{" + tc[1] + "}"
						// Released avions is a stock stopword, but has a real resource-free stem.
						if name == "pg_catalog.french_stem" && tc[0] == "avions" {
							want = "{}"
						}
						for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
							dictionaryResult(t, p, b, e, q, want, tc[0])
						}
					}
					// Revisit the first input on the same held statement after changing tokens.
					firstWant := "{" + group[0][1] + "}"
					if name == "pg_catalog.french_stem" && group[0][0] == "avions" {
						firstWant = "{}"
					}
					dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, firstWant, group[0][0])
					stmt.Close()
				}
			})
		})
	}
}

func TestReferenceFrenchSnowballMorphology(t *testing.T) { frenchCorpus(t, frenchMorphology) }

func TestReferenceFrenchSnowballBoundaries(t *testing.T) {
	frenchCorpus(t, [][2]string{
		{"bababance", "babab"},
		{"babance", "babanc"},
		{"bababissement", "babab"},
		{"babaissement", "babaissement"},
		{"babablogie", "babablog"},
		{"bababusion", "bababu"},
		{"bababution", "bababu"},
		{"bababence", "bababent"},
		{"bababicatrice", "babab"},
		{"babicatrice", "babiqu"},
		{"bababité", "babab"},
		{"bababicité", "babab"},
		{"babicité", "babiqu"},
		{"bababif", "babab"},
		{"bababive", "babab"},
		{"babeuse", "babeux"},
		{"babeusement", "babeux"},
		{"bababamment", "babab"},
		{"bababemment", "bababent"},
		{"bababiment", "babab"},
		{"bababment", "bababment"},
		{"bababir", "babab"},
		{"babaair", "babaair"},
		{"bababions", "babab"},
		{"babions", "babion"},
		{"bababeant", "babab"},
		{"bababtion", "bababt"},
		{"bababxion", "bababxion"},
		{"baption", "baption"},
		{"cé", "cé"},
		{"cèd", "ced"},
		{"CHATS", "chat"},
		{"chats", "chat"},
		{"chat", "chat"},
		{"CAFÉ", "cafÉ"},
		{"CAFÉS", "cafÉ"},
		{"ÉTÉ", "ÉtÉ"},
		{"café", "café"},
		{"café", "caf"},
		{"chats!", "chats!"},
		{"l'", "l'"},
		{"qu'amour", "amour"},
		{"z'amour", "z'amour"},
		{"l’amour", "l’amour"},
		{"l'été", "été"},
		{"bab" + strings.Repeat("a", 995) + "er", "bab" + strings.Repeat("a", 995)},
		{"BAB" + strings.Repeat("A", 996) + "er", "bab" + strings.Repeat("a", 996) + "er"},
		{"ba" + strings.Repeat("é", 498) + "er", "ba" + strings.Repeat("é", 498)},
		{"BAA" + strings.Repeat("é", 498) + "er", "baa" + strings.Repeat("é", 498) + "er"},
	})
}
