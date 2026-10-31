package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNepaliSnowballStopwords(t *testing.T) {
	// Verbatim independent REL_18_6 inventory, never read from native resources.
	words := strings.Fields(`अक्सर
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
	if len(words) != 304 {
		t.Fatal("independent release stopword inventory", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		end := start + 24
		if end > len(words) {
			end = len(words)
		}
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT ts_lexize('nepali_stem',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range group {
					for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
						dictionaryResult(t, p, b, e, q, "{}", word)
					}
				}
			})
		})
	}
	t.Run("resource-isolation-and-order", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TEXT SEARCH DICTIONARY ne_simple (TEMPLATE=simple,StopWords=nepali)",
				"CREATE TEXT SEARCH DICTIONARY ne_plain (TEMPLATE=simple)",
				"CREATE TEXT SEARCH DICTIONARY ne_cross (TEMPLATE=snowball,Language=english,StopWords=nepali)",
				"CREATE TEXT SEARCH DICTIONARY ne_free (TEMPLATE=snowball,Language=nepali)",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			for _, tc := range [][3]string{
				{"ne_simple", "सबै", "{}"}, {"ne_cross", "सबै", "{}"}, {"ne_plain", "सबै", "{सबै}"}, {"ne_free", "सबै", "{सबै}"},
				{"ne_simple", "घरहरू", "{घरहरू}"}, {"ne_cross", "RATS", "{rat}"}, {"ne_free", "घरहरू", "{घर}"},
				{"nepali_stem", "RATS", "{rats}"}, {"nepali_stem", "THE", "{the}"},
				{"nepali_stem", "सबैको", "{सबै}"}, {"nepali_stem", "सबै", "{}"}, {"ne_free", "ले", "{ले}"},
			} {
				nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[2]}}, tc[0], tc[1]))
			}
		})
	})
}
