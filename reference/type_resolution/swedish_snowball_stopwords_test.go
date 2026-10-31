package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSwedishSnowballStopwords(t *testing.T) {
	// Literal REL_18_6 swedish.stop, independent of the embedded resource.
	words := strings.Fields(`och det att i en jag hon som han på den med var sig för så till är men ett
om hade de av icke mig du henne då sin nu har inte hans honom skulle hennes
där min man ej vid kunde något från ut när efter upp vi dem vara vad över
än dig kan sina här ha mot alla under någon eller allt mycket sedan ju denna
själv detta åt utan varit hur ingen mitt ni bli blev oss din dessa några deras
blir mina samma vilken er sådan vår blivit dess inom mellan sådant varför
varje vilka ditt vem vilket sitta sådana vart dina vars vårt våra ert era vilkas`)
	seen := make(map[string]bool)
	for _, word := range words {
		if seen[word] {
			t.Fatal("duplicate in independent released inventory", word)
		}
		seen[word] = true
	}
	if len(words) != 114 {
		t.Fatal("independent released inventory", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		end := min(start+24, len(words))
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const q = "SELECT ts_lexize('swedish_stem',$1::text) AS lexemes"
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
	t.Run("resource-isolation-and-pre-stem-lookup", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY sv_simple (TEMPLATE=simple,StopWords=swedish)",
				"CREATE TEXT SEARCH DICTIONARY sv_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY sv_cross (TEMPLATE=snowball,Language=english,StopWords=swedish)",
				"CREATE TEXT SEARCH DICTIONARY sv_free (TEMPLATE=snowball,Language=swedish)",
				"CREATE TEXT SEARCH DICTIONARY sv_english (TEMPLATE=snowball,Language=swedish,StopWords=english)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"sv_simple", "sitta", "{}"}, {"sv_cross", "sitta", "{}"}, {"sv_plain", "sitta", "{sitta}"}, {"sv_free", "sitta", "{sitt}"},
				{"sv_simple", "HUNDAR", "{hundar}"}, {"sv_cross", "RUNNING", "{run}"}, {"sv_free", "RUNNING", "{running}"},
				{"swedish_stem", "sitta", "{}"}, {"swedish_stem", "sitt", "{sitt}"},
				{"sv_english", "sitta", "{sitt}"}, {"sv_english", "THE", "{}"}, {"sv_english", "RUNNING", "{running}"},
			} {
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
