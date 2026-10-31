package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNorwegianSnowballStopwords(t *testing.T) {
	// Literal REL_18_6 norwegian.stop, including its four duplicated entries.
	words := strings.Fields("og i jeg det at en et den til er som på de med han av ikke ikkje der så var meg seg men ett har om vi min mitt ha hadde hun nå over da ved fra du ut sin dem oss opp man kan hans hvor eller hva skal selv sjøl her alle vil bli ble blei blitt kunne inn når være kom noen noe ville dere som deres kun ja etter ned skulle denne for deg si sine sitt mot å meget hvorfor dette disse uten hvordan ingen din ditt blir samme hvilken hvilke sånn inni mellom vår hver hvem vors hvis både bare enn fordi før mange også slik vært være båe begge siden dykk dykkar dei deira deires deim di då eg ein eit eitt elles honom hjå ho hoe henne hennar hennes hoss hossen ikkje ingi inkje korleis korso kva kvar kvarhelst kven kvi kvifor me medan mi mine mykje no nokon noka nokor noko nokre si sia sidan so somt somme um upp vere vore verte vort varte vart")
	frequencies := make(map[string]int)
	for _, word := range words {
		frequencies[word]++
	}
	if len(words) != 176 || len(frequencies) != 172 {
		t.Fatal("independent released stopword inventory", len(words), len(frequencies))
	}
	for word, count := range frequencies {
		want := 1
		switch word {
		case "ikkje", "si", "som", "være":
			want = 2
		}
		if count != want {
			t.Fatal("release duplicate frequency", word, count, want)
		}
	}
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const q = "SELECT ts_lexize('norwegian_stem',$1::text) AS lexemes"
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
						for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
							dictionaryResult(t, p, b, e, q, "{}", input)
						}
					}
				}
			})
		})
	}
	t.Run("resource-isolation", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY no_simple (TEMPLATE=simple,StopWords=norwegian)",
				"CREATE TEXT SEARCH DICTIONARY no_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY no_cross (TEMPLATE=snowball,Language=english,StopWords=norwegian)",
				"CREATE TEXT SEARCH DICTIONARY no_free (TEMPLATE=snowball,Language=norwegian)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"no_simple", "ikke", "{}"}, {"no_cross", "ikke", "{}"}, {"no_plain", "ikke", "{ikke}"}, {"no_free", "ikke", "{ikk}"},
				{"no_simple", "BILER", "{biler}"}, {"no_cross", "RUNNING", "{run}"}, {"no_free", "RUNNING", "{running}"},
				{"norwegian_stem", "ikke", "{}"}, {"norwegian_stem", "ikkes", "{ikk}"},
			} {
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
