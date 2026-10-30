package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal release-derived expectations; never populated from either backend.
var danishMorphology = [][2]string{
	{"hunde", "hund"}, {"hundene", "hund"}, {"huse", "hus"}, {"huset", "hus"}, {"husets", "hus"},
	{"bilerne", "bil"}, {"biler", "bil"}, {"katte", "kat"}, {"kat", "kat"}, {"arbejde", "arbejd"},
	{"bestemmelse", "bestem"}, {"indtagelse", "indtag"}, {"indtræffe", "indtræf"}, {"undersøgelser", "undersøg"}, {"hurtigst", "hurt"}, {"meningsløst", "meningsløs"},
	{"babhed", "bab"}, {"babethed", "bab"}, {"babered", "bab"}, {"babe", "bab"}, {"baberede", "bab"},
	{"babende", "bab"}, {"baberende", "bab"}, {"babene", "bab"}, {"baberne", "bab"}, {"babere", "bab"},
	{"baben", "bab"}, {"babheden", "bab"}, {"baberen", "bab"}, {"baber", "bab"}, {"babheder", "bab"},
	{"baberer", "bab"}, {"babheds", "bab"}, {"babes", "bab"}, {"babendes", "bab"}, {"baberendes", "bab"},
	{"babenes", "bab"}, {"babernes", "bab"}, {"baberes", "bab"}, {"babens", "bab"}, {"babhedens", "bab"},
	{"baberens", "bab"}, {"babers", "bab"}, {"babets", "bab"}, {"baberets", "bab"}, {"babet", "bab"}, {"baberet", "bab"}, {"babs", "bab"},
	{"babgd", "babg"}, {"babdt", "babd"}, {"babgt", "babg"}, {"babkt", "babk"}, {"friskt", "frisk"}, {"lagt", "lagt"},
	{"babig", "bab"}, {"bablig", "bab"}, {"babelig", "bab"}, {"babels", "babel"}, {"babløst", "babløs"}, {"babgdig", "babg"}, {"babigst", "bab"},
	{"igst", "ig"}, {"ligst", "lig"}, {"babb", "bab"}, {"abb", "abb"}, {"babbb", "babb"}, {"babjj", "babj"}, {"babvv", "babv"},
	{"bab11", "bab11"}, {"babéé", "babéé"}, {"babææ", "babææ"}, {"ben", "ben"}, {"heden", "hed"}, {"bælige", "bæl"}, {"løst", "løst"},
	{"babås", "babå"}, {"babæs", "babæs"}, {"babøs", "babøs"}, {"babus", "babus"}, {"babws", "babws"},
}

func danishCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	for start := 0; start < len(pairs); start += 24 {
		end := start + 24
		if end > len(pairs) {
			end = len(pairs)
		}
		group := pairs[start:end]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY danish_rules (TEMPLATE=pg_catalog.snowball,Language='DaNiSh')", 0)
				for _, name := range []string{"danish_rules", "pg_catalog.danish_stem"} {
					q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, tc := range group {
						for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
							dictionaryResult(t, p, b, e, q, "{"+tc[1]+"}", tc[0])
						}
					}
					stmt.Close()
				}
			})
		})
	}
}

func TestReferenceDanishSnowballMorphology(t *testing.T) { danishCorpus(t, danishMorphology) }

func TestReferenceDanishSnowballBoundaries(t *testing.T) {
	danishCorpus(t, [][2]string{
		{"cd'en", "cd'en"}, {"abc'", "abc'"}, {"hundene!", "hundene!"}, {"hundene\u0301", "hundene\u0301"},
		{"HUNDENE", "hund"}, {"bøger", "bøg"}, {"BØGER", "bØger"}, {"PÅ", "pÅ"}, {"CAFÉS", "cafÉs"}, {"cafés", "cafés"}, {"café", "café"},
		{"bab" + strings.Repeat("a", 995) + "er", "bab" + strings.Repeat("a", 995)},
		{"BAB" + strings.Repeat("A", 996) + "er", "bab" + strings.Repeat("a", 996) + "er"},
		{"bab" + strings.Repeat("æ", 497) + "aer", "bab" + strings.Repeat("æ", 497) + "a"},
		{"BAB" + strings.Repeat("æ", 498) + "er", "bab" + strings.Repeat("æ", 498) + "er"},
	})
}

func TestReferenceDanishSnowballStopwords(t *testing.T) {
	words := strings.Fields("og i jeg det at en den til er som på de med han af for ikke der var mig sig men et har om vi min havde ham hun nu over da fra du ud sin dem os op man hans hvor eller hvad skal selv her alle vil blev kunne ind når være dog noget ville jo deres efter ned skulle denne end dette mit også under have dig anden hende mine alt meget sit sine vor mod disse hvis din nogle hos blive mange ad bliver hendes været thi jer sådan")
	if len(words) != 94 {
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
				q := "SELECT ts_lexize('danish_stem',$1::text) AS lexemes"
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
				"CREATE TEXT SEARCH DICTIONARY da_english_one (TEMPLATE=snowball,Language=english,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY da_danish_one (TEMPLATE=simple,StopWords=danish)",
				"CREATE TEXT SEARCH DICTIONARY da_english_two (TEMPLATE=simple,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY da_danish_two (TEMPLATE=snowball,Language=danish,StopWords=danish)",
				"CREATE TEXT SEARCH DICTIONARY da_cross (TEMPLATE=snowball,Language=english,StopWords=danish)",
				"CREATE TEXT SEARCH DICTIONARY da_free (TEMPLATE=snowball,Language=danish)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range []struct{ name, input, want string }{
				{"da_english_one", "the", "{}"}, {"da_english_one", "og", "{og}"}, {"da_english_two", "the", "{}"}, {"da_english_two", "og", "{og}"},
				{"da_danish_one", "og", "{}"}, {"da_danish_one", "the", "{the}"}, {"da_danish_one", "HUNDENE", "{hundene}"},
				{"da_danish_two", "og", "{}"}, {"da_danish_two", "the", "{the}"}, {"da_cross", "og", "{}"}, {"da_cross", "the", "{the}"},
				{"da_free", "og", "{og}"}, {"da_free", "anden", "{and}"}, {"da_free", "vores", "{vor}"},
				{"danish_stem", "anden", "{}"}, {"danish_stem", "vores", "{vor}"}, {"danish_stem", "vor", "{}"}, {"danish_stem", "På", "{}"}, {"danish_stem", "THE", "{the}"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.name, tc.input)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		})
	})
}
