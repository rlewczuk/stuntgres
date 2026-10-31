package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballNepaliDictionaryResources(t *testing.T) {
	ctx := t.Context()
	t.Run("canceled-uncached-load", func(t *testing.T) {
		dictionaryStopwords.Lock()
		saved, existed := dictionaryStopwords.words["nepali"]
		delete(dictionaryStopwords.words, "nepali")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			defer dictionaryStopwords.Unlock()
			if existed {
				dictionaryStopwords.words["nepali"] = saved
			} else {
				delete(dictionaryStopwords.words, "nepali")
			}
		}()
		loading, cancel := context.WithCancel(ctx)
		defer cancel()
		scan := &germanLoadCancelContext{Context: loading, cancel: cancel}
		d, err := CompileSnowballDictionary(scan, "nepali", "nepali")
		if d != nil || !errors.Is(err, context.Canceled) || !errors.Is(loading.Err(), context.Canceled) {
			t.Fatal("canceled scan published runtime", d, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["nepali"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled scan published backing")
		}
	})
	stock, err := CompileSnowballDictionary(ctx, "nepali", "nepali")
	if err != nil {
		t.Fatal(err)
	}
	again, err := CompileSnowballDictionary(ctx, "nepali", "nepali")
	if err != nil || stock.provider != again.provider || !stock.Configured("nepali", "nepali") || stock.Filtering() {
		t.Fatal("shared stock runtime authority", again, err)
	}
	defaultFirst, err := DefaultNepaliSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	defaultAgain, err := DefaultNepaliSnowballDictionary()
	if err != nil || defaultFirst != defaultAgain || defaultFirst.provider != stock.provider || &defaultFirst.words[0] != &stock.words[0] {
		t.Fatal("default singleton/provider/resource backing", defaultFirst, defaultAgain, err)
	}
	// Independently transcribed complete REL_18_6 nepali.stop.
	stops := strings.Fields(`अक्सर
अगाडि
अगाडी
अझै
अनुसार
अन्तर्गत
अन्य
अन्यथा
अब
अरु
अरू
अर्का
अर्की
अर्को
अर्थात
अर्थात्
अर्ब
अलग
अलिकति
असार
आइतवार
आए
आज
आठ
आत्म
आदि
आफू
आफ्नै
आफ्नो
आयो
आश्विन
उदाहरण
उन
उप
उही
एउटै
एक
एकदम
एकै
ओठ
औं
कति
कतै
कत्रा
कत्री
कत्रो
करोड
कस
कसरी
कसै
कस्ता
कस्ती
कस्तो
कहिल्यै
कहीं
का
कार्तिक
कि
किन
किनभने
कुन
कुनै
कुरा
कृपया
के
केहि
केही
को
कोही
क्रमशः
खर्ब
गयौ
चाँडै
चार
चाले
चाहनुहुन्छ
चाहन्छु
चाहिए
चैत
चौथो
छ
छन्
छु
छू
छैन
छौँ
छौं
जताततै
जति
जत्रा
जत्री
जत्रो
जब
जबकि
जस
जस्ता
जस्ती
जस्तो
जस्तोसुकै
जहाँ
जान
जाहिर
जुन
जे
जेठ
जो
जोसुकै
ठीक
त
तत्काल
तथा
तदनुसार
तपाई
तपाईं
तर
तल
तापनि
तापनी
तिन
तिनी
तिमी
तिर
ती
तीन
तुरुन्तै
तेस्कारण
तेस्रो
त्यसकारण
त्यसपछि
त्यसमा
त्यसैले
त्यहाँ
त्यो
त्सपछि
त्सैले
थिए
थिएन
थिएनन्
थियो
थोरै
दस
दिए
दिनुभएको
दिनुहुन्छ
दुई
देख
देखि
देखिन्छ
देखियो
देखे
देखेको
देखेर
देख्न
दोश्रो
दोस्रो
धेरै
न
नजिकै
नत्र
नयाँ
नि
निम्ति
निम्न
निम्नानुसार
निर्दिष्ट
नै
नौ
पक्का
पक्कै
पछि
पछिल्लो
पटक
पनि
पर्छ
पर्थ्यो
पर्याप्त
पर्सी
पहिले
पहिलो
पहिल्यै
पाँच
पाँचौं
पूर्व
पौष
प्रति
प्रतेक
प्रत्येक
प्रथम
प्लस
फागुन
फेरि
फेरी
बने
बन्द
बन्न
बरु
बाटो
बारे
बाहिर
बाहेक
बिरुद्ध
बिशेष
बिहिवार
बीच
बुधवार
भए
भदौ
भन्
भर
भित्र
भित्री
भोलि
भोली
म
मंसिर
मङ्गलवार
महिना
मा
माघ
मात्र
माथि
मुख्य
मेरो
यति
यत्ति
यत्रा
यत्री
यत्रो
यथोचित
यदि
यद्यपि
यस
यसरी
यसो
यस्ता
यस्ती
यस्तो
यहाँ
या
यिन
यिनी
यी
यो
र
रही
रहेका
रहेको
राखे
राख्छ
राम्रो
रूप
लगभग
लाई
लाख
लागि
ले
वरिपरि
वरीपरी
वर्ष
वास्तवमा
वाहेक
विरुद्ध
विशेष
वैशाख
शनिवार
शायद
शुक्रवार
सँग
सँगै
संग
संगै
सक्छ
सट्टा
सधै
सधैं
सबै
समय
सम्भव
सम्म
सय
सही
साँच्चै
साउन
सात
साथ
साथै
सायद
सारा
सुन्ना
सो
सोध्न
सोमवार
सोही
स्पष्ट
हजार
हप्ता
हरे
हरेक
हामी
हाम्रो
हिजो
हुन्छ
होला
होस्
`)
	sorted := slices.Clone(stops)
	slices.Sort(sorted)
	if len(stops) != 304 || !slices.Equal(stock.words, sorted) {
		t.Fatal("released stopword inventory", len(stops), len(stock.words))
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
				t.Error("flags", lexeme)
			}
		}
	}
	for _, stop := range stops {
		check(stock, stop, "")
		upper := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 'a' + 'A'
			}
			return r
		}, stop)
		check(stock, upper, "")
	}
	for _, tc := range [][2]string{{"सबै", ""}, {"सबैको", "सबै"}, {"घरहरू", "घर"}, {"गर्दै", "गर्"}, {"घरनौँ", "घरनौ"}, {"RATS", "rats"}, {"THE", "the"}, {"CAFÉS", "cafÉs"}, {"café", "café"}, {"", ""},
		{strings.Repeat("A", 994) + "को", strings.Repeat("a", 994)},
		{strings.Repeat("A", 995) + "को", strings.Repeat("a", 995) + "को"},
		{"A" + strings.Repeat("घ", 331) + "को", "a" + strings.Repeat("घ", 331)},
		{"AA" + strings.Repeat("घ", 331) + "को", "aa" + strings.Repeat("घ", 331) + "को"},
	} {
		check(stock, tc[0], tc[1])
	}
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "nepali", "nepali")
		simple, se := CompileSimpleDictionary(ctx, true, "nepali")
		cross, ce := CompileSnowballDictionary(ctx, "english", "nepali")
		if err != nil || se != nil || ce != nil {
			t.Fatal(err, se, ce)
		}
		if &d.words[0] != &stock.words[0] || &simple.words[0] != &stock.words[0] || &cross.words[0] != &stock.words[0] {
			t.Fatal("same resource does not share immutable backing")
		}
		check(cross, "सबै", "")
	}
	for _, name := range []string{"english", "danish", "dutch", "german"} {
		other, err := CompileSnowballDictionary(ctx, "nepali", name)
		if err != nil || &other.words[0] == &stock.words[0] {
			t.Fatal("distinct resource backing", name, err)
		}
	}
	free, err := CompileSnowballDictionary(ctx, "nepali", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "सबै", "सबै")
	mixed, err := CompileSnowballDictionary(ctx, "NePaLi", "nepali")
	if err != nil || mixed.provider != stock.provider || !mixed.Configured("NePaLi", "nepali") || mixed.Configured("nepali", "nepali") {
		t.Fatal("configured spelling/provider authority", err)
	}
	forged := *stock
	counterfeit := *stock.provider
	forged.provider = &counterfeit
	if forged.Configured("nepali", "nepali") {
		t.Fatal("copied public language spelling forged registered provider authority")
	}
	for _, language := range []string{"", " nepali ", "Nepali ", "nepali_missing"} {
		if d, err := CompileSnowballDictionary(ctx, language, "nepali"); d != nil || err == nil {
			t.Fatal("unregistered language accepted", language, d, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "nepali", "nepali"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := stock.Lexize(canceled, "घरहरू"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"nepali_missing", "Nepali", "../nepali"} {
		if d, err := CompileSnowballDictionary(ctx, "nepali", name); d != nil || err == nil {
			t.Fatal(name, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["nepali_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["nepali"]
	dictionaryStopwords.Unlock()
	if missing || empty || len(backing) != 304 || &backing[0] != &stock.words[0] {
		t.Fatal("failed/empty load poisoned backing")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "घरहरू", "घर")
				check(stock, "सबै", "")
				check(free, "सबै", "सबै")
			}
		})
	}
	wg.Wait()
}
