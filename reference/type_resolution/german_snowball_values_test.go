package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent same-line vocabulary/output pairs, not native-generated goldens:
// https://raw.githubusercontent.com/snowballstem/snowball-data/1702e6ee5ac904abdedfd6efe45b3e69059125a9/german/voc.txt
// https://raw.githubusercontent.com/snowballstem/snowball-data/1702e6ee5ac904abdedfd6efe45b3e69059125a9/german/output.txt
// Pair line provenance is recorded in the German research handoff.
var germanMorphology = [][2]string{
	{"abbildung", "abbild"},
	{"aufenthalt", "aufenthalt"},
	{"bäcker", "back"},
	{"bedürfnissen", "bedurfnis"},
	{"beendigung", "beendig"},
	{"billigkeit", "billig"},
	{"billigung", "billig"},
	{"derbsten", "derb"},
	{"eilig", "eilig"},
	{"eisig", "eisig"},
	{"feuer", "feu"},
	{"freundlichkeit", "freundlich"},
	{"gearbeitet", "gearbeitet"},
	{"handeln", "handel"},
	{"häuser", "haus"},
	{"holen", "hol"},
	{"holt", "holt"},
	{"hunde", "hund"},
	{"hunden", "hund"},
	{"kategorie", "kategori"},
	{"katzen", "katz"},
	{"kaufst", "kauf"},
	{"lehrerin", "lehr"},
	{"möglichkeiten", "moglich"},
	{"planet", "planet"},
	{"quae", "qua"},
	{"quelle", "quell"},
	{"schnellsten", "schnell"},
	{"strasse", "strass"},
	{"straße", "strass"},
	{"system", "system"},
}

func germanCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	for start := 0; start < len(pairs); start += 24 {
		end := start + 24
		if end > len(pairs) {
			end = len(pairs)
		}
		group := pairs[start:end]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY german_rules (TEMPLATE=pg_catalog.snowball,Language='GeRmAn')", 0)
				for _, name := range []string{"german_rules", "pg_catalog.german_stem"} {
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

func TestReferenceGermanSnowballMorphology(t *testing.T) { germanCorpus(t, germanMorphology) }

// Synthetic region, prelude and release-negative literals are [INFERENCE]
// from REL_18_6; root must resolve them with the pinned oracle before acceptance.
func TestReferenceGermanSnowballBoundaries(t *testing.T) {
	germanCorpus(t, [][2]string{
	{"aben", "aben"},
	{"abben", "abb"},
	{"äben", "aben"},
	{"äbben", "abb"},
	{"abigkeit", "abig"},
	{"babig", "babig"},
	{"bababig", "babab"},
	{"abs", "abs"},
	{"babs", "bab"},
	{"bababs", "babab"},
	{"bababxs", "bababxs"},
	{"ablns", "ablns"},
	{"babst", "babst"},
	{"bababst", "babab"},
	{"babarst", "babarst"},
	{"bababet", "bababet"},
	{"anna's", "anna's"},
	{"bababeig", "bababeig"},
	{"bababigung", "babab"},
	{"bababeigung", "bababeig"},
	{"bababerlich", "babab"},
	{"bababenheit", "babab"},
	{"bababigkeit", "babab"},
	{"babigkeit", "babig"},
	{"haeuser", "haus"},
	{"schoen", "schon"},
	{"schön", "schon"},
	{"ueber", "uber"},
	{"fuer", "fur"},
	{"ä", "a"},
	{"Ä", "Ä"},
	{"café", "café"},
	{"ä", "ä"},
	{"HAUSER", "haus"},
	{"HäUSER", "haus"},
	{"HÄUSER", "hÄus"},
	{"FÜR", "fÜr"},
	{"ÜBER", "Über"},
	{"STRAßE", "strass"},
	{"STRAẞE", "straẞ"},
	{"babern", "bab"},
	{"baberin", "bab"},
	{"baberinnen", "bab"},
	{"babln", "babl"},
	{"bablns", "babl"},
	{"babem", "bab"},
	{"babnisses", "babnis"},
	{"bababend", "babab"},
	{"babend", "babend"},
	{"bababik", "babab"},
	{"babik", "babik"},
	{"bababeik", "bababeik"},
	{"bababisch", "babab"},
	{"babisch", "babisch"},
	{"bababeisch", "bababeisch"},
		{"bab" + strings.Repeat("a", 995) + "bs", "bab" + strings.Repeat("a", 995) + "b"},
		{"BAB" + strings.Repeat("A", 996) + "bs", "bab" + strings.Repeat("a", 996) + "bs"},
		{"BAB" + strings.Repeat("ä", 498) + "bs", "bab" + strings.Repeat("ä", 498) + "bs"},
	})
}

func TestReferenceGermanSnowballStopwords(t *testing.T) {
	// Literal REL_18_6 german.stop, independent of embedded production bytes.
	words := strings.Fields("aber alle allem allen aller alles als also am an ander andere anderem anderen anderer anderes anderm andern anderr anders auch auf aus bei bin bis bist da damit dann der den des dem die das daß derselbe derselben denselben desselben demselben dieselbe dieselben dasselbe dazu dein deine deinem deinen deiner deines denn derer dessen dich dir du dies diese diesem diesen dieser dieses doch dort durch ein eine einem einen einer eines einig einige einigem einigen einiger einiges einmal er ihn ihm es etwas euer eure eurem euren eurer eures für gegen gewesen hab habe haben hat hatte hatten hier hin hinter ich mich mir ihr ihre ihrem ihren ihrer ihres euch im in indem ins ist jede jedem jeden jeder jedes jene jenem jenen jener jenes jetzt kann kein keine keinem keinen keiner keines können könnte machen man manche manchem manchen mancher manches mein meine meinem meinen meiner meines mit muss musste nach nicht nichts noch nun nur ob oder ohne sehr sein seine seinem seinen seiner seines selbst sich sie ihnen sind so solche solchem solchen solcher solches soll sollte sondern sonst über um und uns unse unsem unsen unser unses unter viel vom von vor während war waren warst was weg weil weiter welche welchem welchen welcher welches wenn werde werden wie wieder will wir wird wirst wo wollen wollte würde würden zu zum zur zwar zwischen")
	if len(words) != 231 {
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
				q := "SELECT ts_lexize('german_stem',$1::text) AS lexemes"
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
				"CREATE TEXT SEARCH DICTIONARY de_simple (TEMPLATE=simple,StopWords=german)",
				"CREATE TEXT SEARCH DICTIONARY de_cross (TEMPLATE=snowball,Language=english,StopWords=german)",
				"CREATE TEXT SEARCH DICTIONARY de_free (TEMPLATE=snowball,Language=german)",
				"CREATE TEXT SEARCH DICTIONARY de_english (TEMPLATE=snowball,Language=german,StopWords=english)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range []struct{ name, input, want string }{
				{"de_simple", "UND", "{}"}, {"de_simple", "HäUSER", "{häuser}"},
				{"de_cross", "UND", "{}"}, {"de_cross", "THE", "{the}"},
				{"de_free", "UND", "{und}"}, {"de_english", "UND", "{und}"},
				{"de_english", "THE", "{}"}, {"german_stem", "THE", "{the}"},
				{"german_stem", "für", "{}"}, {"german_stem", "über", "{}"},
				{"german_stem", "daß", "{}"}, {"german_stem", "fuer", "{fur}"},
				{"german_stem", "ueber", "{uber}"}, {"german_stem", "dass", "{dass}"},
				{"german_stem", "DASS", "{dass}"}, {"german_stem", "FÜR", "{fÜr}"},
				{"german_stem", "ÜBER", "{Über}"},
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
