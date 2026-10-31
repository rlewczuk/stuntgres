package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations trace the pinned release and include root-observed PG18.6
// oracle corrections (tests-1); never native output or production suffix tables.
func TestReferenceTurkishSnowballCorpus(t *testing.T) {
	t.Run("natural-and-postlude", func(t *testing.T) {
		turkishCorpus(t, [][2]string{
			{"ev", "ev"}, {"evler", "ev"}, {"evlar", "evlar"},
			{"kitaplar", "kitap"}, {"kitapları", "kitap"}, {"kitabı", "kitap"},
			{"ağacı", "ağaç"}, {"evdeki", "ev"}, {"evdekiler", "evdeki"},
			{"doktoruymuşsunuz", "doktor"}, {"kedim", "kedi"},
			{"ad", "ad"}, {"soyad", "soyad"}, {"abab", "abap"},
			{"abac", "abaç"}, {"abağ", "abak"}, {"abad", "abadı"},
			{"abeg", "abegi"}, {"abog", "abogu"}, {"abög", "abögü"},
			{"abablar", "abab"}, {"ab", "ab"}, {"âbâlar", "âbâlar"},
		})
	})
	for _, family := range []struct{ prefix, suffixes string }{
		{"abax", "lar ları ı ın a da ta dan tan la ca ım ımız ınız dır mış dı dım dın dık sa sam san sak ken sın sınız ız dınız mışcasına ında ına ından ını daki ınki ındaki"},
		{"ebex", "ler leri i in e de den le ce im imiz iniz dir miş di sem sin siniz iz deki"},
		{"obox", "u un um umuz unuz dur muş du sun sunuz uz"},
		{"öböx", "ü ün üm ümüz ünüz dür müş dü sün sünüz üz"},
		// Remaining voiced/unvoiced yDU and DUr alternatives from the pinned
		// marker definitions; ysA/possessives/sUnUz deliberately lack harmony.
		{"abax", "tı tım tın tık tır"},
		{"ebex", "ti tim tin tik dim din dik tir sen sek siniz"},
		{"obox", "tu tum tun tuk dum dun duk tur"},
		{"öböx", "tü tüm tün tük düm dün dük tür"},
	} {
		t.Run(family.prefix, func(t *testing.T) {
			var cases [][2]string
			for _, suffix := range strings.Fields(family.suffixes) {
				cases = append(cases, [2]string{family.prefix + suffix, family.prefix})
			}
			turkishCorpus(t, cases)
		})
	}
	t.Run("ordered-branches-and-committed-deletions", func(t *testing.T) {
		// Nominal cAsInA requires ymUs_; failure leaves the original span
		// available to noun alternatives. lAr disables the entire postlude.
		// Noun ncA/DAn/nUn/ylA delete their outer span before optional
		// inner chains; ndA/nA/ndAn/nU instead require a preceding branch.
		turkishCorpus(t, [][2]string{
			{"abaxmışcasına", "abax"}, {"abaxmışsınızcasına", "abax"},
			{"abaxmışlar", "abax"}, {"abaxdılar", "abax"},
			{"abaxsalar", "abax"}, {"abaxdırlar", "abax"},
			{"abaxdınız", "abax"}, {"abaxsanız", "abax"},
			{"abaxmışsınız", "abax"}, {"abaxmışız", "abax"},
			{"abaxmışımdır", "abax"}, {"abaxsındır", "abaxs"},
			{"abaxlarınca", "abax"}, {"abaxımca", "abax"},
			{"abaxlarca", "abax"}, {"abaxlarına", "abax"},
			{"abaxsına", "abaxs"}, {"abaxlarından", "abax"},
			// The sU arm commits the case deletion, then removes lAr.
			{"abaxlarını", "abax"},
			{"abaxımdan", "abax"}, {"abaxlardan", "abax"},
			{"abaxların", "abax"}, {"abaxımla", "abax"},
			{"abaxlarla", "abax"}, {"abaxları", "abax"},
			{"abaxımda", "abax"}, {"abaxlarda", "abax"},
			{"abaxlarım", "abax"}, {"abaxdakiler", "abaxdaki"},
			{"abaxdakilerdeki", "abax"}, {"abaxlarınki", "abaxlar"},
			{"abaxımdaki", "abax"}, {"abaxlarındaki", "abax"},
		})
	})
	t.Run("harmony-buffer-rejection-and-rollback", func(t *testing.T) {
		// Optional y/s/n present requires a preceding vowel. When absent,
		// test(next vowel) examines TWO preceding characters, not one.
		// Harmony goto can skip an incompatible nearer vowel to find an
		// earlier compatible vowel: this is not a linguistic validator.
		turkishCorpus(t, [][2]string{
			{"abaxler", "abaxler"}, {"ebexlar", "ebexlar"},
			{"abaxdir", "abaxdir"}, {"abaxmiş", "abaxmiş"},
			{"abaxyı", "abaxyı"}, {"abaxsı", "abaxsı"},
			{"abaxnca", "abaxnca"}, {"abaxnın", "abaxn"},
			{"abaxki", "abaxki"}, {"abaxndaki", "abaxn"},
			{"abaxxyzı", "abaxxyzı"}, {"abaxxyzım", "abaxxyz"},
			{"abaxyken", "abaxyke"}, {"abaxcasına", "abaxca"},
			{"abaxsinız", "abaxs"}, {"abaxsiniz", "abax"},
			{"abaexlar", "abaex"}, {"abaxse", "abax"},
			{"abayı", "aba"}, {"abası", "abas"}, {"abanın", "aba"},
			{"abayla", "aba"}, {"abayken", "ap"},
			// Explicit y buffers and optional high-vowel present/absent paths.
			{"abayım", "ap"}, {"abayız", "ap"}, {"abaydı", "ap"},
			{"abaymış", "ap"}, {"abaysa", "ap"}, {"abam", "aba"},
			{"abamız", "aba"}, {"abaxm", "abaxm"},
			{"abasına", "aba"}, {"abasında", "aba"}, {"abasından", "aba"},
			{"abasını", "aba"}, {"abaxınınki", "abax"},
			{"abaxımın", "abax"},
		})
	})
}

func turkishCorpus(t *testing.T, cases [][2]string) {
	t.Helper()
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY turkish_corpus (TEMPLATE=snowball,Language=turkish)", 0)
		const query = "SELECT ts_lexize('turkish_corpus',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
					dictionaryResult(t, p, b, e, query, "{"+tc[1]+"}", tc[0])
				}
			})
		}
	})
}
