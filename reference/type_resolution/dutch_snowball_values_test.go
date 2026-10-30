package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical Porter algorithm from PostgreSQL REL_18_6, not modern Dutch.
// Independently traced literals, pending pinned-oracle discovery; never populated
// from the native provider or its suffix tables.
var dutchMorphology = [][2]string{
	{"honden", "hond"},
	{"HONDEN", "hond"},
	{"bomen", "bom"},
	{"huizen", "huiz"},
	{"katten", "kat"},
	{"mannen", "mann"},
	{"mussen", "muss"},
	{"fietsje", "fietsj"},
	{"fietsjes", "fietsjes"},
	{"gewerkt", "gewerkt"},
	{"maan", "man"},
	{"brood", "brod"},
	{"buur", "bur"},
	{"been", "ben"},
	{"lichtje", "lichtj"},
	{"lichtjes", "lichtjes"},
	{"lichamelijkheden", "licham"},
	{"bababheden", "babab"},
	{"babheden", "babheid"},
	{"bababen", "babab"},
	{"bababene", "babab"},
	{"babakken", "babak"},
	{"babadden", "babad"},
	{"babatten", "babat"},
	{"babben", "babb"},
	{"babagemen", "babagemen"},
	{"bababaen", "bababaen"},
	{"bababs", "babab"},
	{"bababse", "babab"},
	{"bababjs", "bababjs"},
	{"bababys", "bababys"},
	{"bababjse", "bababjs"},
	{"bas", "bas"},
	{"babs", "bab"},
	{"heden", "heden"},
	{"abheden", "abheden"},
	{"bababe", "babab"},
	{"babakke", "babak"},
	{"babaae", "babaae"},
	{"bababheid", "babab"},
	{"bababcheid", "bababcheid"},
	{"bababing", "babab"},
	{"bababend", "babab"},
	{"bababig", "babab"},
	{"bababeig", "bababeig"},
	{"bababiging", "babab"},
	{"bababeiging", "bababeig"},
	{"bababkking", "bababk"},
	{"bababelijk", "babab"},
	{"bababbaar", "babab"},
	{"bababbar", "bababbar"},
	{"bababbare", "babab"},
	{"ababelijk", "abab"},
	{"baaien", "baai"},
	{"äben", "aben"},
	{"äbben", "abb"},
}

func dutchCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	for start := 0; start < len(pairs); start += 24 {
		end := start + 24
		if end > len(pairs) {
			end = len(pairs)
		}
		group := pairs[start:end]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dutch_rules (TEMPLATE=pg_catalog.snowball,Language='DuTcH')", 0)
				for _, name := range []string{"dutch_rules", "pg_catalog.dutch_stem"} {
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

func TestReferenceDutchSnowballMorphology(t *testing.T) { dutchCorpus(t, dutchMorphology) }

func TestReferenceDutchSnowballBoundaries(t *testing.T) {
	dutchCorpus(t, [][2]string{
		{"dé", "de"}, {"café", "caf"}, {"cafés", "cafes"}, {"CAFÉ", "cafÉ"}, {"CAFÉS", "cafÉ"},
		{"cafe\u0301", "cafe\u0301"}, {"café!", "cafe!"},
		{"bab" + strings.Repeat("a", 995) + "bs", "bab" + strings.Repeat("a", 995) + "b"},
		{"BAB" + strings.Repeat("A", 996) + "bs", "bab" + strings.Repeat("a", 996) + "bs"},
		{"bab" + strings.Repeat("ä", 497) + "abs", "bab" + strings.Repeat("a", 498) + "b"},
		{"BAB" + strings.Repeat("ä", 498) + "bs", "bab" + strings.Repeat("ä", 498) + "bs"},
	})
}

func TestReferenceDutchSnowballStopwords(t *testing.T) {
	words := strings.Fields("de en van ik te dat die in een hij het niet zijn is was op aan met als voor had er maar om hem dan zou of wat mijn men dit zo door over ze zich bij ook tot je mij uit der daar haar naar heb hoe heeft hebben deze u want nog zal me zij nu ge geen omdat iets worden toch al waren veel meer doen toen moet ben zonder kan hun dus alles onder ja eens hier wie werd altijd doch wordt wezen kunnen ons zelf tegen na reeds wil kon niets uw iemand geweest andere")
	if len(words) != 101 {
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
				q := "SELECT ts_lexize('dutch_stem',$1::text) AS lexemes"
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
				"CREATE TEXT SEARCH DICTIONARY nl_english_one (TEMPLATE=snowball,Language=english,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY nl_dutch_one (TEMPLATE=simple,StopWords=dutch)",
				"CREATE TEXT SEARCH DICTIONARY nl_danish (TEMPLATE=snowball,Language=danish,StopWords=danish)",
				"CREATE TEXT SEARCH DICTIONARY nl_dutch_two (TEMPLATE=snowball,Language=dutch,StopWords=dutch)",
				"CREATE TEXT SEARCH DICTIONARY nl_english_two (TEMPLATE=simple,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY nl_cross (TEMPLATE=snowball,Language=english,StopWords=dutch)",
				"CREATE TEXT SEARCH DICTIONARY nl_free (TEMPLATE=snowball,Language=dutch)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, name := range []string{"nl_english_one", "nl_dutch_one", "nl_danish", "nl_dutch_two", "nl_english_two", "nl_dutch_one", "nl_english_one"} {
				q := "SELECT ts_lexize('" + name + "',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, input := range []string{"THE", "DE", "OG"} {
					want := "{" + strings.ToLower(input) + "}"
					if (strings.Contains(name, "english") && input == "THE") || (strings.Contains(name, "dutch") && input == "DE") || (name == "nl_danish" && (input == "DE" || input == "OG")) {
						want = "{}"
					}
					for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
						dictionaryResult(t, p, b, e, q, want, input)
					}
				}
				stmt.Close()
			}
			for _, tc := range []struct{ name, input, want string }{
				{"nl_dutch_one", "HONDEN", "{honden}"}, {"nl_dutch_two", "HONDEN", "{hond}"},
				{"nl_cross", "DE", "{}"}, {"nl_cross", "THE", "{the}"},
				{"nl_free", "DE", "{de}"}, {"dutch_stem", "OG", "{og}"},
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
