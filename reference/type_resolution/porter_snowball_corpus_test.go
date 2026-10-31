package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent fixed pairs: snowball-data 4f982ec072a091771b321ab7861841471942e14a
// porter/{voc,output}.txt; SQL preserves nonempty s when the kernel returns empty.
func TestReferencePorterSnowballMorphology(t *testing.T) {
	porterCorpus(t, [][2]string{
		{"added", "ad"}, {"agreed", "agre"}, {"as", "a"}, {"bled", "bled"},
		{"caresses", "caress"}, {"controll", "control"}, {"dying", "dy"}, {"enjoy", "enjoi"},
		{"failing", "fail"}, {"feed", "feed"}, {"generously", "gener"}, {"happy", "happi"},
		{"hopping", "hop"}, {"is", "i"}, {"lying", "ly"}, {"news", "new"},
		{"only", "onli"}, {"playing", "plai"}, {"ponies", "poni"}, {"rate", "rate"},
		{"roll", "roll"}, {"running", "run"}, {"s", "s"}, {"sing", "sing"},
		{"skies", "ski"}, {"sky", "sky"}, {"ties", "ti"}, {"tying", "ty"}, {"us", "u"},
	})
}

func porterCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY porter_corpus (TEMPLATE=snowball,Language=porter)", 0)
		const query = "SELECT ts_lexize('porter_corpus',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()

		for _, literal := range [][2]string{{"skies", "ski"}, {"dying", "dy"}, {"news", "new"}, {"s", "s"}} {
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('porter_corpus','"+literal[0]+"') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + literal[1] + "}"}}))
		}
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
					dictionaryResult(t, p, b, e, query, "{"+tc[1]+"}", tc[0])
				}
			})
		}
	})
}
