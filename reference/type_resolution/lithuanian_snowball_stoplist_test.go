package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func lithuanianEnglishStoplist(t *testing.T) {
	// Literal released English127 list; Lithuanian keeps its own stemmer when a
	// foreign stoplist is explicitly configured. Stock Lithuanian has no stoplist.
	words := strings.Fields("i me my myself we our ours ourselves you your yours yourself yourselves he him his himself she her hers herself it its itself they them their theirs themselves what which who whom this that these those am is are was were be been being have has had having do does did doing a an the and but if or because as until while of at by for with about against between into through during before after above below to from up down in out on off over under again further then once here there when where why how all any both each few more most other some such no nor not only own same so than too very s t can will just don should now")
	if len(words) != 127 {
		t.Fatal("independent English stoplist", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		group := words[start:min(start+24, len(words))]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY lithuanian_stops (TEMPLATE=snowball,Language=lithuanian,StopWords=english)", 0)
				q := "SELECT ts_lexize('lithuanian_stops',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range group {
					dictionaryResult(t, p, b, c, q, "{}", word)
					dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{}", strings.ToUpper(word))
				}
			})
		})
	}
}