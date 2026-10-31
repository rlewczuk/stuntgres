package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Historical pairs: snowball-data da07690d20ce69ee16b5fb95796f92090cc9b4a1,
// yiddish/{voc,output}.txt, credited to Simcha Taub/YIVO and Assaf Urieli,
// dual GPLv3+/CC-BY-SA 3.0. Only these small attributed literal excerpts are used.
// Synthetic cases below follow Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece,
// algorithms/yiddish.sbl and PostgreSQL REL_18_6 stem_UTF_8_yiddish.c.
// They specify full public pipeline results, not a future kernel's tables.
func TestReferenceYiddishSnowballMorphology(t *testing.T) {
	groups := []struct {
		name string
		pairs [][2]string
	}{
		{"historical", [][2]string{
			{"אַ", "א"}, {"אַבֿידה", "אבידה"}, {"אַבֿידות", "אבידה"},
			{"אַבאָניר", "אבאניר"}, {"אַבאָנירט", "אבאניר"}, {"אַבאָנירן", "אבאניר"},
			{"אַבסטראַקציע", "אבסטראקציע"}, {"אַבסטראַקציעס", "אבסטראקציע"},
			{"אַגיטאַציע", "אגיטאציע"}, {"אַגיטאַציעס", "אגיטאציע"},
			{"אַדורכגעביסן", "אדורכבײס"}, {"אַדורכגעביסנס", "אדורכביס"},
			{"אַדורכגעגאַנגען", "אדורכגײ"}, {"אַדורכגעשריבן", "אדורכשרײב"},
			{"אַדורכגעשריבנס", "אדורכשריב"}, {"גע", "גע"}, {"געבן", "געב"},
			{"געבראַכט", "ברענג"}, {"געגאַנגען", "גײ"}, {"גײן", "גײ"},
			{"גענומען", "נעמ"}, {"געשריבן", "שרײב"}, {"געשריבנס", "שריב"},
			{"קינדער", "קינד"}, {"מענטשן", "מענטש"}, {"און", "אונ"},
			{"אין", "אינ"}, {"דער", "דער"}, {"הױז", "הױז"}, {"בליק", "בליק"}, {"גליק", "גליק"},
		}},
		// The same five-letter root places every candidate beyond fixed p1.
		// Primary pass: every ordinary among candidate, including overlaps.
		{"primary-inflection", [][2]string{
			{"זאבאבערס", "זאבאב"}, {"זאבאבענ", "זאבאב"}, {"זאבאבן", "זאבאב"}, {"זאבאבנס", "זאבאב"},
			{"זאבאבענערס", "זאבאב"}, {"זאבאבעס", "זאבאב"}, {"זאבאבע", "זאבאב"},
			{"זאבאבנ", "זאבאב"}, {"זאבאבס", "זאבאב"}, {"זאבאבעם", "זאבאב"}, {"זאבאבער", "זאבאב"},
			{"זאבאביע", "זאבאביע"}, {"זאבאביעס", "זאבאביע"},
			{"זאבאבענע", "זאבאב"}, {"זאבאבענעם", "זאבאב"}, {"זאבאבענער", "זאבאב"}, {"זאבאבענס", "זאבאב"},
			{"זאבאבט", "זאבאב"}, {"זאבאבטנ", "זאבאב"},
			{"זאבאבטענער", "זאבאב"}, {"זאבאבטענעם", "זאבאב"}, {"זאבאבטענע", "זאבאב"},
			{"זאבאבטנס", "זאבאב"}, {"זאבאבטער", "זאבאב"}, {"זאבאבטע", "זאבאב"}, {"זאבאבטס", "זאבאב"},
			{"זאבאבעט", "זאבאב"}, {"זאבאבעטנ", "זאבאב"}, {"זאבאבעטס", "זאבאב"},
			{"זאבאבעטע", "זאבאב"}, {"זאבאבעטער", "זאבאב"},
		}},
		{"primary-derivation", [][2]string{
			{"זאבאבונג", "זאבאב"}, {"זאבאבהײט", "זאבאב"}, {"זאבאבקײט", "זאבאב"},
			{"זאבאביקײט", "זאבאב"}, {"זאבאבשאפט", "זאבאב"},
			{"זאבאביזמ", "זאבאב"}, {"זאבאביזמען", "זאבאב"}, {"זאבאבים", "זאבאב"}, {"זאבאבות", "זאבאבה"},
			{"זאבאבעלעכ", "זאבאב"}, {"זאבאבעלע", "זאבאב"}, {"זאבאבלעכ", "זאבאב"},
			{"זאבאבעלעס", "זאבאב"}, {"זאבאבעלען", "זאבאב"},
			{"זאבאביסט", "זאבאב"}, {"זאבאביסטנ", "זאבאב"}, {"זאבאבסטו", "זאבאב"},
			{"זאבאבסטער", "זאבאב"}, {"זאבאבסטע", "זאבאב"}, {"זאבאבסטנ", "זאבאב"}, {"זאבאבסט", "זאבאב"},
		}},
		// Unconditional primary irregulars, including the three literal GE
		// candidates. Real גע inputs must pass marking and final marker cleanup.
		{"primary-irregular", [][2]string{
			{"גײן", "גײ"}, {"געגאנגען", "גײ"}, {"גענומען", "נעמ"}, {"געשריבנ", "שרײב"},
			{"געמיטנ", "מײד"}, {"געביטנ", "בײט"}, {"געביסנ", "בײס"},
			{"געװיזנ", "װײז"}, {"געטריבנ", "טרײב"}, {"געליטנ", "לײט"},
			{"געקליבנ", "קלײב"}, {"געריבנ", "רײב"}, {"געריסנ", "רײס"},
			{"געשװיגנ", "שװײג"}, {"געשמיסנ", "שמײס"}, {"געשניטנ", "שנײד"},
			{"געבונדנ", "בינד"}, {"געװוטשנ", "װיטש"}, {"געזונגנ", "זינג"},
			{"געטרונקנ", "טרינק"}, {"געצװונגנ", "צװינג"}, {"געשלונגנ", "שלינג"},
			{"געבױגנ", "בײג"}, {"געהױבנ", "הײב"}, {"פארלױרנ", "פארליר"},
			{"שטאנען", "שטײ"}, {"געשװױרנ", "שװער"}, {"געבראכט", "ברענג"},
		}},
		// The nested rewrite after ene/enem/ener/ens must also be reached.
		// Tes+ene/ener is a longer primary match: it shadows the nested
		// irregular action, retaining Tes when that match starts before R1.
		{"primary-adjectival-irregular", [][2]string{
			{"געגאנגענע", "גײ"}, {"גענומענעם", "נעמ"}, {"געמיטענער", "מיט"}, {"געמיטענס", "מײד"},
			{"געביטענס", "בײט"}, {"געביסענע", "בײס"}, {"געװיזענע", "װײז"},
			{"געטריבענע", "טרײב"}, {"געליטענע", "ליט"}, {"געליטענס", "לײט"}, {"געקליבענע", "קלײב"},
			{"געריבענע", "רײב"}, {"געריסענע", "רײס"}, {"געשװיגענע", "שװײג"},
			{"געשמיסענע", "שמײס"}, {"געשניטענע", "שני"}, {"געשניטענס", "שנײד"}, {"געשריבענע", "שרײב"},
			{"געבונדענע", "בינד"}, {"געװוטשענע", "װיטש"}, {"געזונגענע", "זינג"},
			{"געטרונקענע", "טרינק"}, {"געצװונגענע", "צװינג"}, {"געשלונגענע", "שלינג"},
			{"געבױגענע", "בײג"}, {"געהױבענע", "הײב"}, {"פארלױרענע", "פארליר"},
			{"שטאנענע", "שטײ"}, {"געשװױרענע", "שװער"}, {"געבראכטענע", "ברענג"},
		}},
		{"secondary-and-tertiary", [][2]string{
			// Tes+e consumes Tes in the first pass, leaving no hayt/kayt/
			// ikayt/shaft candidate for the second pass. The en variants
			// below expose each complete secondary candidate instead.
			{"זאבאבונגע", "זאבאב"}, {"זאבאבהײטע", "זאבאבהײ"}, {"זאבאבקײטע", "זאבאבקײ"},
			{"זאבאביקײטע", "זאבאביקײ"}, {"זאבאבשאפטע", "זאבאבשאפ"}, {"זאבאבל", "זאבאב"},
			{"זאבאבהײטענ", "זאבאב"}, {"זאבאבקײטענ", "זאבאב"},
			{"זאבאביקײטענ", "זאבאב"}, {"זאבאבשאפטענ", "זאבאב"},
			{"זאבאבלע", "זאבאב"}, {"זאבאביל", "זאבאביל"},
			{"זאבאביג", "זאבאב"}, {"זאבאביק", "זאבאב"}, {"זאבאביש", "זאבאב"},
			{"זאבאבניק", "זאבאב"}, {"זאבאבדיק", "זאבאב"}, {"זאבאבנדיק", "זאבאב"},
			{"זאבאבענדיק", "זאבאב"}, {"זאבאבבליק", "זאבאבבליק"}, {"זאבאבגליק", "זאבאבגליק"},
			{"זאבאביקײטנ", "זאבאביקײ"}, {"זאבאבישונגס", "זאבאב"},
			{"זאבאבונגיק", "זאבאבונג"},
		}},
		// Outside R1 a longest match must not fall back to a shorter among.
		// Tes inflections deliberately rewrite to Tes even when deletion fails.
		{"primary-region-negatives", [][2]string{
			{"ערס", "ערס"}, {"ענ", "ענ"}, {"נס", "נס"}, {"ענערס", "ענערס"},
			{"עס", "עס"}, {"ע", "ע"}, {"נ", "נ"}, {"ס", "ס"}, {"עמ", "עמ"}, {"ער", "ער"},
			{"יע", "יע"}, {"יעס", "יעס"}, {"ענע", "ענע"}, {"ענעמ", "ענעמ"}, {"ענער", "ענער"}, {"ענס", "ענס"},
			{"ט", "ט"}, {"טנ", "ט"}, {"טע", "ט"}, {"טער", "ט"}, {"טס", "ט"},
			{"טנס", "ט"}, {"טענע", "ט"}, {"טענעמ", "ט"}, {"טענער", "ט"},
			{"עט", "עט"}, {"עטנ", "עטנ"}, {"עטס", "עטס"}, {"עטע", "עטע"}, {"עטער", "עטער"},
			{"ונג", "ונג"}, {"הײט", "הײט"}, {"קײט", "קײט"}, {"יקײט", "יקײט"}, {"שאפט", "שאפט"},
			{"יזמ", "יזמ"}, {"יזמענ", "יזמענ"}, {"ימ", "ימ"}, {"ות", "ות"},
			{"עלעכ", "עלעכ"}, {"עלע", "עלע"}, {"לעכ", "לעכ"}, {"עלעס", "עלעס"}, {"עלענ", "עלענ"},
			{"יסט", "יסט"}, {"יסטנ", "יסטנ"}, {"סטו", "סטו"}, {"סטער", "סטער"}, {"סטע", "סטע"}, {"סטנ", "סטנ"}, {"סט", "סט"},
			{"מיטנ", "מיט"}, {"ביטנ", "ביט"}, {"ביסנ", "ביס"}, {"ליטנ", "ליט"}, {"ריסנ", "ריס"},
		}},
		{"later-pass-region-negatives", [][2]string{
			{"ל", "ל"}, {"אל", "אל"}, {"איג", "איג"}, {"איק", "איק"}, {"איש", "איש"},
			{"ניק", "ניק"}, {"דיק", "דיק"}, {"נדיק", "נדיק"}, {"ענדיק", "ענדיק"},
			{"בליק", "בליק"}, {"גליק", "גליק"}, {"אונג", "אונג"},
		}},
		{"regions-and-failed-actions", [][2]string{
			{"אער", "אער"}, {"אטנ", "אט"}, {"איסט", "איסט"},
			{"גיסט", "גיס"}, {"שיסט", "שיס"}, {"אבגיסט", "אבגיס"}, {"אבשיסט", "אבשיס"},
			{"גיסטנ", "גיסטנ"}, {"בליק", "בליק"}, {"גליק", "גליק"},
			{"שפרנ", "שפרנ"}, {"שטרנ", "שטרנ"}, {"שטשנ", "שטשנ"}, {"דזששנ", "דזששנ"},
			{"ברכנ", "ברכ"}, {"שפרינגנ", "שפרינג"},
			{"געא", "א"}, {"געב", "ב"}, {"גע", "גע"}, {"געלט", "געל"}, {"געבנ", "געב"},
			{"אױסצוגנ", "אױסצוג"}, {"אױסצוקט", "אױסצוק"}, {"אױסצוקנ", "אױסצוק"},
			{"אװעקגעבנ", "אװעקגעב"}, {"אױסצוזאבאבנ", "אױסזאבאב"},
			{"אױסגעזאבאבנ", "אױסזאבאב"}, {"זאבאבGE", "זאבאבge"}, {"זאבאבTSU", "זאבאבtsu"},
		}},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) { yiddishCorpus(t, group.pairs) })
	}
	t.Run("verbal-prefixes", func(t *testing.T) {
		// All released prefix candidates; expected root and suffix are literal,
		// prefix preservation is independent of how a kernel represents markers.
		for _, prefix := range []string{"אדורכ", "דורכ", "אהינ", "אהער", "אװעק", "מיט", "אנטקעגנ", "אקעגנ", "אנידער", "אראפ", "ארױס", "ארױפ", "ארומ", "ארײנ", "ארונטער", "אריבער", "נאכ", "פארבײ", "אהײמ", "אפיר", "פארױס", "פונאנדער", "צוזאמענ", "צונױפ", "צוריק", "אױס", "אױפ", "אומ", "אונטער", "איבער", "אײנ", "אנ", "אפ", "בײ", "פאר", "צו", "אנט", "בא", "דער", "צע"} {
			t.Run(prefix, func(t *testing.T) {
				yiddishCorpus(t, [][2]string{{prefix + "געזאבאבנ", prefix + "זאבאב"}, {prefix + "צוזאבאבנ", prefix + "זאבאב"}})
			})
		}
	})
}

func yiddishCorpus(t *testing.T, pairs [][2]string) {
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
			dictionaryResult(t, p, b, c, q, want, "yiddish_stem", pair[0])
			for range 2 { dictionaryResult(t, p, b, held, q, want, "yiddish_stem", pair[0]) }
		}
	})
}

func TestReferenceYiddishSnowballBoundaries(t *testing.T) {
	yiddishCorpus(t, [][2]string{
		{"וו", "װ"}, {"ווּ", "וו"}, {"וי", "ױ"}, {"ויִ", "וי"}, {"יי", "ײ"}, {"ייִ", "יי"},
		{"ךםןףץ", "כמנפצ"}, {"אךבםגףדץ", "אכבמגפדצ"},
		{"א\u05b0\u05b1\u05b2\u05b3\u05b4\u05b5\u05b6\u05b7\u05b8\u05b9\u05bb\u05bc\u05bf\u05c1\u05c2", "א"},
		// A nonempty token whose kernel result is empty retains its original
		// C-lowered spelling in dsnowball_lexize, rather than a stopword result.
		{"\u05b0", "\u05b0"}, {"\u05bc\u05bf", "\u05bc\u05bf"},
		{"א\u05ba\u05c7", "א\u05ba\u05c7"}, {"אַ", "אַ"}, {"é", "é"}, {"e\u0301", "e\u0301"},
		{"RATS", "rats"}, {"ONLY", "only"}, {"THE", "the"}, {"RUNNING", "running"}, {"CAFÉS", "cafÉs"}, {"café", "café"},
		{"A B", "a b"}, {"קינדער!", "קינדער!"}, {"GE", "ge"}, {"TSU", "tsu"},
		{strings.Repeat("A", 998) + "ך", strings.Repeat("a", 998) + "כ"},
		{strings.Repeat("A", 999) + "ך", strings.Repeat("a", 999) + "ך"},
	})
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize('yiddish_stem','') AS lexemes", "SELECT ts_lexize('yiddish_stem',NULL::text) AS lexemes", "SELECT ts_lexize(NULL::regdictionary,'קינדער') AS lexemes"} {
			var want any
			if strings.Contains(q, "''") { want = "{}" }
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{want}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
