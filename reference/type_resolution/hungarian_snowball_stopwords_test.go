package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHungarianSnowballStopwords(t *testing.T) {
	// Literal REL_18_6 hungarian.stop, independent of embedded production bytes.
	words := strings.Fields("a ahogy ahol aki akik akkor alatt által általában amely amelyek amelyekben amelyeket amelyet amelynek ami amit amolyan amíg amikor át abban ahhoz annak arra arról az azok azon azt azzal azért aztán azután azonban bár be belül benne cikk cikkek cikkeket csak de e eddig egész egy egyes egyetlen egyéb egyik egyre ekkor el elég ellen elő először előtt első én éppen ebben ehhez emilyen ennek erre ez ezt ezek ezen ezzel ezért és fel felé hanem hiszen hogy hogyan igen így illetve ill. ill ilyen ilyenkor ison ismét itt jó jól jobban kell kellett keresztül keressünk ki kívül között közül legalább lehet lehetett legyen lenne lenni lesz lett maga magát majd majd már más másik meg még mellett mert mely melyek mi mit míg miért milyen mikor minden mindent mindenki mindig mint mintha mivel most nagy nagyobb nagyon ne néha nekem neki nem néhány nélkül nincs olyan ott össze ő ők őket pedig persze rá s saját sem semmi sok sokat sokkal számára szemben szerint szinte talán tehát teljes tovább továbbá több úgy ugyanis új újabb újra után utána utolsó vagy vagyis valaki valami valamint való vagyok van vannak volt voltam voltak voltunk vissza vele viszont volna")
	if len(words) != 199 {
		t.Fatal("independent stopword inventory", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT ts_lexize('hungarian_stem',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range group {
					upper := strings.Map(func(r rune) rune {
						if r >= 'a' && r <= 'z' {
							return r - 'a' + 'A'
						}
						return r
					}, word)
					for _, input := range []string{word, upper} {
						t.Run(input, func(t *testing.T) {
							for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
								dictionaryResult(t, p, b, e, q, "{}", input)
							}
						})
					}
				}
			})
		})
	}
	t.Run("resource-isolation", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY hu_simple (TEMPLATE=simple,StopWords=hungarian)",
				"CREATE TEXT SEARCH DICTIONARY hu_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY hu_cross (TEMPLATE=snowball,Language=english,StopWords=hungarian)",
				"CREATE TEXT SEARCH DICTIONARY hu_free (TEMPLATE=snowball,Language=hungarian)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"hu_simple", "cikk", "{}"}, {"hu_cross", "cikk", "{}"}, {"hu_plain", "cikk", "{cikk}"}, {"hu_free", "cikk", "{cik}"},
				// aholban is not stopped; case ban yields the stopped word ahol.
				// A second stopword lookup after stemming would incorrectly erase it.
				{"hungarian_stem", "ahol", "{}"}, {"hungarian_stem", "aholban", "{ahol}"},
				{"hungarian_stem", "cikkben", "{cik}"}, {"hungarian_stem", "AZ", "{}"}, {"hungarian_stem", "és", "{}"}, {"hungarian_stem", "ÉS", "{És}"}, {"hungarian_stem", "ő", "{}"}, {"hungarian_stem", "Ő", "{Ő}"},
			} {
				hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
