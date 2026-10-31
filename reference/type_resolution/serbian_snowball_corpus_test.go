package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical inputs and baseline pairs from snowball-data
// 8a2cdafcfde47bdb01bd9f5646a822fa73b94b75, serbian/{voc,output}.txt
// (Wikipedia-derived CC-BY-SA 3.0 corpus):
// https://github.com/snowballstem/snowball-data/tree/8a2cdafcfde47bdb01bd9f5646a822fa73b94b75/serbian
// Expected stems follow the released Serbian SBL at Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece, bundled in REL_18_6,
// rather than assuming every historical output is release-matched.
// PostgreSQL 18.6 confirms abnormalnost→abnormaln (Step_3 ost deletion)
// and lozi→loga (both globally selected loga/oga cross fixed p1=2).
// Release Step_2 ostima→ost succeeds for abnormalnostima, so Step_3
// must not subsequently delete the newly exposed ost.
func TestReferenceSerbianSnowballMorphology(t *testing.T) {
	groups := [][][2]string{
		{{"abadžija", "abadžij"}, {"abadžijama", "abadžij"}, {"abadžije", "abadžij"},
		 {"abadžijska", "abadžijsk"}, {"abadžijskih", "abadžijsk"},
		 {"abakus", "abakus"}, {"abakusa", "abakus"}, {"abakusom", "abakus"}, {"abakusu", "abakus"}},
		{{"abdicira", "abdicir"}, {"abdiciraju", "abdicir"}, {"abdicirala", "abdicir"},
		 {"abdiciranja", "abdiciranj"}, {"abdiciranjem", "abdiciran"}, {"abdiciraće", "abdicirać"},
		 {"abnormalan", "abnormaln"}, {"abnormalna", "abnormaln"}, {"abnormalnost", "abnormaln"},
		 {"abnormalnosti", "abnormalnost"}, {"abnormalnostima", "abnormalnost"}, {"acetat", "aceta"}, {"acetata", "acetat"}},
	}
	for i, pairs := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) { serbianCorpus(t, pairs) })
	}
}

func serbianCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, pair := range pairs {
			want := "{" + pair[1] + "}"
			if strings.ContainsAny(pair[1], " \t") { want = "{\"" + pair[1] + "\"}" }
			dictionaryResult(t, p, b, c, q, want, "serbian_stem", pair[0])
			for range 2 { dictionaryResult(t, p, b, held, q, want, "serbian_stem", pair[0]) }
		}
	})
}

func TestReferenceSerbianSnowballBoundaries(t *testing.T) {
	serbianCorpus(t, [][2]string{
		{"rad", "rad"}, {"рад", "rad"}, {"РАД", "РАД"}, {"ABAKUSA", "abakus"},
		{"bijeb", "beb"}, {"bjeb", "beb"}, {"djb", "đb"},
		{"bijebijeb", "bebeb"}, {"bjebjeb", "bebeb"},
		{"ba", "ba"}, {"baba", "bab"}, {"rba", "rba"}, {"rbba", "rbb"},
		{"lozi", "loga"}, {"pesi", "peh"}, {"čb", "čb"}, {"ČB", "Čb"},
		{"c\u030cb", "c\u030cb"}, {"abakusa!", "abakusa!"},
		{"!ijeb", "!ijeb"}, {"bijex", "bijex"}, {"xijeb", "xijeb"}, {"bje!", "bje!"},
		{strings.Repeat("б", 500), strings.Repeat("b", 500)},
		{strings.Repeat("б", 500) + "A", strings.Repeat("б", 500) + "a"},
		{strings.Repeat("B", 1000), strings.Repeat("b", 1000)},
		{strings.Repeat("B", 1001), strings.Repeat("b", 1001)},
	})
}

func TestReferenceSerbianSnowballStopwords(t *testing.T) {
	t.Run("english127", serbianEnglishStoplist)
	serbianCorpus(t, [][2]string{{"A", "a"}, {"I", "i"}, {"THE", "the"}, {"AND", "and"}, {"ONLY", "only"}})
}
