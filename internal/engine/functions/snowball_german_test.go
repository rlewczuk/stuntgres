package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func germanUnitLower(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}, s)
}

// Cancel a real context during resource scanning, not through a production hook.
type germanLoadCancelContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
}

func (c *germanLoadCancelContext) Err() error {
	c.calls++
	if c.calls == 8 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSnowballGermanRules(t *testing.T) {
	// Independent paired Snowball data and REL_18_6 boundaries, also covered
	// publicly; ASCII lower here supplies the kernel's actual wrapper contract.
	for _, tc := range [][2]string{
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
	{"auae", "aua"},
	{"ayae", "aya"},
	{"aue", "aue"},
	{"ue", "u"},
	{"aßen", "ass"},
	{"ß", "ss"},
	{"babablichkeit", "babab"},
	{"bablichkeit", "bablich"},
	{"bababest", "babab"},
	{"babest", "bab"},
	{"babsystem", "babsystem"},
	{"bababnis", "bababnis"},
	{"bababung", "babab"},
	{"babung", "babung"},
	} {
		if got := germanStem(germanUnitLower(tc[0])); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	t.Run("regions", func(t *testing.T) {
		for _, tc := range []struct { word string; r1, r2 int }{
			{"", 0, 0}, {"a", 1, 1}, {"ab", 2, 2}, {"abigkeit", 3, 4},
			{"äben", 3, 4}, {"bababig", 3, 5}, {"aeiou", 5, 5},
		} {
			r1, r2 := germanRegions([]rune(tc.word))
			if r1 != tc.r1 || r2 != tc.r2 {
				t.Fatal(tc, r1, r2)
			}
		}
	})
}

func TestSnowballGermanResources(t *testing.T) {
	ctx := t.Context()
	t.Run("canceled-uncached-load", func(t *testing.T) {
		// Temporarily detach only the cache entry. Already compiled runtimes
		// retain their immutable backing, and cleanup restores its exact identity.
		dictionaryStopwords.Lock()
		saved, existed := dictionaryStopwords.words["german"]
		delete(dictionaryStopwords.words, "german")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			defer dictionaryStopwords.Unlock()
			if existed {
				dictionaryStopwords.words["german"] = saved
			} else {
				delete(dictionaryStopwords.words, "german")
			}
		}()
		loading, cancel := context.WithCancel(ctx)
		defer cancel()
		scan := &germanLoadCancelContext{Context: loading, cancel: cancel}
		d, err := CompileSnowballDictionary(scan, "german", "german")
		if d != nil || !errors.Is(err, context.Canceled) || !errors.Is(loading.Err(), context.Canceled) {
			t.Fatal("canceled load published a runtime", d, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["german"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled resource scan published cache backing")
		}
	})
	german, err := DefaultGermanSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	resources := make(map[string]*SnowballDictionary)
	for _, tc := range []struct { name string; count int }{{"german", 231}, {"english", 127}, {"danish", 94}, {"dutch", 101}} {
		d, err := CompileSnowballDictionary(ctx, tc.name, tc.name)
		if err != nil || len(d.words) != tc.count {
			t.Fatal(tc, d, err)
		}
		for name, other := range resources {
			if &d.words[0] == &other.words[0] {
				t.Fatal("distinct resources share backing", name, tc.name)
			}
		}
		resources[tc.name] = d
	}
	stops := strings.Fields("aber alle allem allen aller alles als also am an ander andere anderem anderen anderer anderes anderm andern anderr anders auch auf aus bei bin bis bist da damit dann der den des dem die das daß derselbe derselben denselben desselben demselben dieselbe dieselben dasselbe dazu dein deine deinem deinen deiner deines denn derer dessen dich dir du dies diese diesem diesen dieser dieses doch dort durch ein eine einem einen einer eines einig einige einigem einigen einiger einiges einmal er ihn ihm es etwas euer eure eurem euren eurer eures für gegen gewesen hab habe haben hat hatte hatten hier hin hinter ich mich mir ihr ihre ihrem ihren ihrer ihres euch im in indem ins ist jede jedem jeden jeder jedes jene jenem jenen jener jenes jetzt kann kein keine keinem keinen keiner keines können könnte machen man manche manchem manchen mancher manches mein meine meinem meinen meiner meines mit muss musste nach nicht nichts noch nun nur ob oder ohne sehr sein seine seinem seinen seiner seines selbst sich sie ihnen sind so solche solchem solchen solcher solches soll sollte sondern sonst über um und uns unse unsem unsen unser unses unter viel vom von vor während war waren warst was weg weil weiter welche welchem welchen welcher welches wenn werde werden wie wieder will wir wird wirst wo wollen wollte würde würden zu zum zur zwar zwischen")
	if len(stops) != 231 {
		t.Fatal("independent released inventory", len(stops))
	}
	sorted := slices.Clone(stops)
	slices.Sort(sorted)
	if !slices.Equal(german.words, sorted) {
		t.Fatal("released resource contents")
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
			return
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("lexeme flags", lexeme)
			}
		}
	}
	for _, stop := range stops {
		check(german, stop, "")
		upper := strings.Map(func(c rune) rune { if c >= 'a' && c <= 'z' { return c - 'a' + 'A' }; return c }, stop)
		check(german, upper, "")
	}
	for _, tc := range [][2]string{
		{"HäUSER", "haus"}, {"HÄUSER", "hÄus"}, {"FÜR", "fÜr"}, {"ÜBER", "Über"}, {"fuer", "fur"}, {"dass", "dass"}, {"daß", ""},
		{"A B", "a b"}, {"", ""},
		{"bab" + strings.Repeat("a", 995) + "bs", "bab" + strings.Repeat("a", 995) + "b"},
		{"BAB" + strings.Repeat("A", 996) + "bs", "bab" + strings.Repeat("a", 996) + "bs"},
		{"BAB" + strings.Repeat("ä", 498) + "bs", "bab" + strings.Repeat("ä", 498) + "bs"},
	} {
		check(german, tc[0], tc[1])
	}
	for range 3 {
		for name, original := range resources {
			d, err := CompileSnowballDictionary(ctx, name, name)
			if err != nil {
				t.Fatal(err)
			}
			s, err := CompileSimpleDictionary(ctx, true, name)
			if err != nil || &s.words[0] != &original.words[0] || &d.words[0] != &original.words[0] {
				t.Fatal("same-resource immutable backing", name, err)
			}
		}
	}
	cross, err := CompileSnowballDictionary(ctx, "english", "german")
	if err != nil || &cross.words[0] != &german.words[0] {
		t.Fatal("cross-provider resource", err)
	}
	check(cross, "UND", "")
	check(cross, "THE", "the")
	free, err := CompileSnowballDictionary(ctx, "german", "")
	if err != nil || free.words != nil {
		t.Fatal("list-free German", free, err)
	}
	check(free, "UND", "und")
	mixed, err := CompileSnowballDictionary(ctx, "GeRmAn", "german")
	if err != nil || mixed.provider != german.provider || !mixed.Configured("GeRmAn", "german") || mixed.Configured("german", "german") {
		t.Fatal("provider spelling/authority", mixed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "german", "german"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := german.Lexize(canceled, "HäUSER"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, resource := range []string{"german_missing", "German", "../german"} {
		if d, err := CompileSnowballDictionary(ctx, "german", resource); d != nil || err == nil {
			t.Fatal(resource, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["german_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["german"]
	dictionaryStopwords.Unlock()
	if missing || empty || len(backing) != 231 || &backing[0] != &german.words[0] {
		t.Fatal("failed/empty compilation poisoned cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(german, "HäUSER", "haus")
				check(german, "UND", "")
				check(resources["english"], "THE", "")
				check(resources["danish"], "OG", "")
				check(resources["dutch"], "DE", "")
			}
		})
	}
	wg.Wait()
}
