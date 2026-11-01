package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-derived REL_18_6 goldens; neither backend supplies expected stems.
var finnishMorphology = [][2]string{
	{"talo", "talo"},
	{"talot", "talo"},
	{"talon", "talo"},
	{"taloon", "talo"},
	{"talossa", "talo"},
	{"talosta", "talo"},
	{"talolla", "talo"},
	{"talolta", "talo"},
	{"talolle", "talo"},
	{"talona", "talo"},
	{"taloksi", "talo"},
	{"taloni", "talo"},
	{"talosi", "talo"},
	{"talonsa", "talo"},
	{"talomme", "talo"},
	{"talonne", "talo"},
	{"talossaan", "talo"},
	{"talolleen", "talo"},
	{"taloineen", "talo"},
	{"taloissa", "talo"},
	{"taloista", "talo"},
	{"taloihin", "talo"},
	{"talojen", "talo"},
	{"taloja", "talo"},
	{"talohan", "talo"},
	{"talokin", "talo"},
	{"talokaan", "talo"},
	{"taloko", "talo"},
	{"talopa", "talo"},
	{"talohon", "talo"},
	{"kahvi", "kahv"},
	{"metsässä", "mets"},
	{"kylässä", "kylä"},
	{"kädet", "käde"},
	{"katu", "katu"},
	{"kadut", "kadu"},
	{"talous", "talous"},
	{"bababukin", "bababu"},
	{"bababukaan", "bababu"},
	{"bababukään", "bababu"},
	{"bababuko", "bababu"},
	{"bababukö", "bababu"},
	{"bababuhan", "bababu"},
	{"bababuhän", "bababu"},
	{"bababupa", "bababu"},
	{"bababupä", "bababu"},
	{"bababusi", "bababu"},
	{"bababuni", "bababu"},
	{"bababunsa", "bababu"},
	{"bababunsä", "bababu"},
	{"bababumme", "bababu"},
	{"bababunne", "bababu"},
	{"bababua", "bababu"},
	{"bababuä", "bababu"},
	{"bababuta", "bababu"},
	{"bababutä", "bababu"},
	{"bababussa", "bababu"},
	{"bababussä", "bababu"},
	{"bababusta", "bababu"},
	{"bababustä", "bababu"},
	{"bababulla", "bababu"},
	{"bababullä", "bababu"},
	{"bababulta", "bababu"},
	{"bababultä", "bababu"},
	{"bababulle", "bababu"},
	{"bababuna", "bababu"},
	{"bababunä", "bababu"},
	{"bababuksi", "bababu"},
	{"bababuine", "bababu"},
	{"bababumpi", "bababu"},
	{"bababumpa", "bababu"},
	{"bababumpä", "bababu"},
	{"bababummi", "bababu"},
	{"bababumma", "bababu"},
	{"bababummä", "bababu"},
	{"bababuimpi", "bababu"},
	{"bababuimpa", "bababu"},
	{"bababuimpä", "bababu"},
	{"bababuimmi", "bababu"},
	{"bababuimma", "bababu"},
	{"bababuimmä", "bababu"},
	{"bababueja", "bababu"},
	{"bababuejä", "bababu"},
	{"bababutaan", "bababu"},
	{"bababussaan", "bababu"},
	{"bababustaan", "bababu"},
	{"bababullaan", "bababu"},
	{"bababultaan", "bababu"},
	{"bababunaan", "bababu"},
	{"bababutään", "bababu"},
	{"bababussään", "bababu"},
	{"bababustään", "bababu"},
	{"bababullään", "bababu"},
	{"bababultään", "bababu"},
	{"bababunään", "bababu"},
	{"bababulleen", "bababu"},
	{"bababuineen", "bababu"},
	{"bababukseni", "bababu"},
	{"bababuimmat", "bababu"},
	{"bababsti", "babab"},
	{"babasti", "babast"},
	{"bababetta", "babab"},
	{"bababettä", "babab"},
	{"bababutta", "bababut"},
	{"bababuttä", "bababut"},
	{"bababahan", "babab"},
	{"bababehen", "babab"},
	{"bababihin", "babab"},
	{"bababohon", "bababo"},
	{"bababähän", "babab"},
	{"bababöhön", "bababö"},
	{"bababuin", "bababu"},
	{"bababuuden", "bababuud"},
	{"bababupompi", "bababupomp"},
	{"bababummat", "bababu"},
	{"bababupommat", "bababupom"},
	// Original among predicates may fall back to n; postselection hVn may not.
	{"babaiden", "baba"}, {"babaitten", "baba"}, {"babaisiin", "baba"},
	{"babaaden", "babaad"}, {"babayiden", "babayid"},
	{"babaaseen", "baba"}, {"babayyseen", "babayys"},
	{"babxhan", "babxhan"}, {"babxhin", "babxhin"}, {"babxhon", "babxhon"},
	{"babxhän", "babxhän"}, {"babxhön", "babxhön"},
	// Fixed-region and predecessor failures remain observable through later stages.
	{"ankin", "an"}, {"akin", "ak"}, {"ababsti", "abab"}, {"abasti", "abast"},
	{"bababkin", "bababk"}, {"bababkaan", "bababk"}, {"bababkään", "bababk"},
	{"bababko", "bababko"}, {"bababkö", "bababkö"},
	{"bababhan", "bababhan"}, {"bababhän", "bababhän"},
	{"bababpa", "bababp"}, {"bababpä", "bababp"},
	{"babaksi", "baba"}, {"bababuqaan", "bababuq"}, {"bababuqään", "bababuq"},
	{"bababuqeen", "bababuq"}, {"bababuiet", "bababuie"},
	// LONG membership and tidy order, not arbitrary accented vowels/consonants.
	{"bababaa", "babab"}, {"bababee", "babab"}, {"bababii", "babab"},
	{"bababoo", "bababo"}, {"bababuu", "bababu"}, {"bababää", "babab"},
	{"bababöö", "bababö"}, {"bababyy", "bababyy"},
	{"bababoj", "bababo"}, {"bababuj", "bababu"}, {"bababjo", "bababj"},
	{"bababé", "bababé"}, {"bababø", "bababø"}, {"babéé", "babéé"},
	{"babää", "babä"}, {"baböö", "babö"},
}

func finnishCorpus(t *testing.T, pairs [][2]string) {
	t.Helper()
	for start := 0; start < len(pairs); start += 24 {
		end := start + 24
		if end > len(pairs) {
			end = len(pairs)
		}
		group := pairs[start:end]
		t.Run(group[0][0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY finnish_rules (TEMPLATE=pg_catalog.snowball,Language='FiNnIsH')", 0)
				for _, name := range []string{"finnish_rules", "pg_catalog.finnish_stem"} {
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

func TestReferenceFinnishSnowballMorphology(t *testing.T) { finnishCorpus(t, finnishMorphology) }

func TestReferenceFinnishSnowballBoundaries(t *testing.T) {
	finnishCorpus(t, [][2]string{
		{"talohankin", "talohan"}, {"talohun", "talohu"}, {"babuhun", "babuhu"},
		{"babxhen", "babxhen"}, {"babøhön", "babøhön"}, {"talo'", "talo'"},
		{"aarteisiin", "aart"}, {"adressiin", "adres"}, {"bab11", "bab11"},
		{"babéé", "babéé"}, {"babåå", "babåå"}, {"yy", "yy"}, {"bb", "b"}, {"babbb", "babb"},
		{"talossa!", "talossa!"}, {"talossa\u0301", "talossa\u0301"},
		{"TALOSSA", "talo"}, {"KAHVI", "kahv"}, {"talÖssa", "talÖ"},
		{"café", "café"}, {"CAFÉ", "cafÉ"}, {"METSÄSSÄ", "metsÄssÄ"},
		{"HÄN", "hÄn"}, {"TÄSSÄ", "tÄssÄ"}, {"metsa\u0308ssa\u0308", "metsa\u0308ssa\u0308"},
		{"bab" + strings.Repeat("a", 994) + "ssa", "bab" + strings.Repeat("a", 993)},
		{"BAB" + strings.Repeat("A", 995) + "ssa", "bab" + strings.Repeat("a", 995) + "ssa"},
		{"bab" + strings.Repeat("ä", 497) + "ssa", "bab" + strings.Repeat("ä", 496)},
		{"BAB" + strings.Repeat("ä", 497) + "assa", "bab" + strings.Repeat("ä", 497) + "assa"},
	})
}
func TestReferenceFinnishSnowballStopwords(t *testing.T) {
	words := strings.Fields(`olla olen olet on olemme olette ovat ole oli olisi olisit olisin olisimme olisitte olisivat olit olin olimme olitte olivat ollut olleet en et ei emme ette eivät minä minun minut minua minussa minusta minuun minulla minulta minulle sinä sinun sinut sinua sinussa sinusta sinuun sinulla sinulta sinulle hän hänen hänet häntä hänessä hänestä häneen hänellä häneltä hänelle me meidän meidät meitä meissä meistä meihin meillä meiltä meille te teidän teidät teitä teissä teistä teihin teillä teiltä teille he heidän heidät heitä heissä heistä heihin heillä heiltä heille tämä tämän tätä tässä tästä tähän tallä tältä tälle tänä täksi tuo tuon tuotä tuossa tuosta tuohon tuolla tuolta tuolle tuona tuoksi se sen sitä siinä siitä siihen sillä siltä sille sinä siksi nämä näiden näitä näissä näistä näihin näillä näiltä näille näinä näiksi nuo noiden noita noissa noista noihin noilla noilta noille noina noiksi ne niiden niitä niissä niistä niihin niillä niiltä niille niinä niiksi kuka kenen kenet ketä kenessä kenestä keneen kenellä keneltä kenelle kenenä keneksi ketkä keiden ketkä keitä keissä keistä keihin keillä keiltä keille keinä keiksi mikä minkä minkä mitä missä mistä mihin millä miltä mille minä miksi mitkä joka jonka jota jossa josta johon jolla jolta jolle jona joksi jotka joiden joita joissa joista joihin joilla joilta joille joina joiksi että ja jos koska kuin mutta niin sekä sillä tai vaan vai vaikka kanssa mukaan noin poikki yli kun niin nyt itse`)
	if len(words) != 235 {
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
				q := "SELECT ts_lexize('finnish_stem',$1::text) AS lexemes"
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
					for _, input := range []string{word, upper, strings.Map(func(r rune) rune {
						if r >= 'a' && r <= 'm' {
							return r - 'a' + 'A'
						}
						return r
					}, word)} {
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
				"CREATE TEXT SEARCH DICTIONARY fi_english (TEMPLATE=snowball,Language=english,StopWords=finnish)",
				"CREATE TEXT SEARCH DICTIONARY fi_simple (TEMPLATE=simple,StopWords=finnish)",
				"CREATE TEXT SEARCH DICTIONARY fi_cross (TEMPLATE=snowball,Language=finnish,StopWords=english)",
				"CREATE TEXT SEARCH DICTIONARY fi_free (TEMPLATE=snowball,Language=finnish)",
				"CREATE TEXT SEARCH DICTIONARY fi_again (TEMPLATE=snowball,Language=finnish,StopWords=finnish)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range []struct{ name, input, want string }{
				{"fi_english", "ON", "{}"}, {"fi_simple", "ON", "{}"}, {"fi_simple", "TALOSSA", "{talossa}"},
				{"fi_cross", "THE", "{}"}, {"fi_cross", "ON", "{}"}, {"fi_again", "ON", "{}"},
				{"fi_free", "on", "{on}"}, {"fi_free", "olen", "{ole}"}, {"fi_free", "olla", "{ol}"},
				{"finnish_stem", "onkin", "{on}"}, {"finnish_stem", "tällä", "{täl}"}, {"finnish_stem", "tuota", "{tuota}"},
				{"finnish_stem", "tallä", "{}"}, {"finnish_stem", "tuotä", "{}"}, {"finnish_stem", "THE", "{the}"},
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
