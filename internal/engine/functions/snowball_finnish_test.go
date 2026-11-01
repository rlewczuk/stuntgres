package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballFinnishRules(t *testing.T) {
	for _, tc := range [][2]string{
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
	} {
		if got := finnishStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestSnowballFinnishRegions(t *testing.T) {
	for _, tc := range []struct {
		word   string
		r1, r2 int
	}{{"talo", 3, 4}, {"bababu", 3, 5}, {"bb", 2, 2}, {"yy", 2, 2}, {"kylässä", 3, 5}} {
		a, b := finnishRegions([]rune(tc.word))
		if a != tc.r1 || b != tc.r2 {
			t.Fatal(tc, a, b)
		}
	}
}

func TestSnowballFinnishResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultFinnishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	stops := strings.Fields(`olla olen olet on olemme olette ovat ole oli olisi olisit olisin olisimme olisitte olisivat olit olin olimme olitte olivat ollut olleet en et ei emme ette eivät minä minun minut minua minussa minusta minuun minulla minulta minulle sinä sinun sinut sinua sinussa sinusta sinuun sinulla sinulta sinulle hän hänen hänet häntä hänessä hänestä häneen hänellä häneltä hänelle me meidän meidät meitä meissä meistä meihin meillä meiltä meille te teidän teidät teitä teissä teistä teihin teillä teiltä teille he heidän heidät heitä heissä heistä heihin heillä heiltä heille tämä tämän tätä tässä tästä tähän tallä tältä tälle tänä täksi tuo tuon tuotä tuossa tuosta tuohon tuolla tuolta tuolle tuona tuoksi se sen sitä siinä siitä siihen sillä siltä sille sinä siksi nämä näiden näitä näissä näistä näihin näillä näiltä näille näinä näiksi nuo noiden noita noissa noista noihin noilla noilta noille noina noiksi ne niiden niitä niissä niistä niihin niillä niiltä niille niinä niiksi kuka kenen kenet ketä kenessä kenestä keneen kenellä keneltä kenelle kenenä keneksi ketkä keiden ketkä keitä keissä keistä keihin keillä keiltä keille keinä keiksi mikä minkä minkä mitä missä mistä mihin millä miltä mille minä miksi mitkä joka jonka jota jossa josta johon jolla jolta jolle jona joksi jotka joiden joita joissa joista joihin joilla joilta joille joina joiksi että ja jos koska kuin mutta niin sekä sillä tai vaan vai vaikka kanssa mukaan noin poikki yli kun niin nyt itse`)
	if len(stops) != 235 || len(d.words) != 235 {
		t.Fatal("released input lines", len(stops), len(d.words))
	}
	check := func(input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || (want == "" && len(got.Lexemes) != 0) || (want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want)) {
			t.Error(input, got, want, err)
		}
	}
	for _, s := range stops {
		check(s, "")
		check(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 'a' + 'A'
			}
			return r
		}, s), "")
	}
	simple, err := CompileSimpleDictionary(ctx, true, "finnish")
	if err != nil {
		t.Fatal(err)
	}
	again, err := CompileSnowballDictionary(ctx, "FiNnIsH", "finnish")
	if err != nil {
		t.Fatal(err)
	}
	english, err := DefaultSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	danish, err := DefaultDanishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if &simple.words[0] != &d.words[0] || &again.words[0] != &d.words[0] || &english.words[0] == &d.words[0] || &danish.words[0] == &d.words[0] || again.provider != d.provider || !again.Configured("FiNnIsH", "finnish") || again.Configured("finnish", "finnish") {
		t.Fatal("resource/provider backing")
	}
	check("onkin", "on")
	check("tällä", "täl")
	check("tuota", "tuota")
	check("HÄN", "hÄn")
	check("TALOSSA", "talo")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := CompileSnowballDictionary(canceled, "finnish", "finnish"); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	for _, resource := range []string{"finnish_missing", "Finnish", "../finnish"} {
		if got, err := CompileSnowballDictionary(ctx, "finnish", resource); got != nil || err == nil {
			t.Fatal(got, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["finnish_missing"]
	dictionaryStopwords.Unlock()
	if missing {
		t.Fatal("failed resource published")
	}
	free, err := CompileSnowballDictionary(ctx, "finnish", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	for _, tc := range [][2]string{{"ON", "on"}, {"OLEN", "ole"}, {"OLLA", "ol"}} {
		got, err := free.Lexize(ctx, tc[0])
		if err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != tc[1] {
			t.Fatal(got, err)
		}
	}
	// The shared immutable runtime is safe across concurrent invocations.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check("TALOSSA", "talo")
				check("ON", "")
				check("babxhen", "babxhen")
			}
		})
	}
	wg.Wait()
	// Empty kernel output retains the lowered token through the shared wrapper.
	empty := &SnowballDictionary{provider: &snowballLanguage{name: "unit", stem: func(string) string { return "" }}}
	got, err := empty.Lexize(ctx, "ABC")
	if err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "abc" {
		t.Fatal(got, err)
	}
}
