package functions

import (
	"strings"
	"testing"
)

// Corpus expectations derive from the released algorithm, independently of
// the wrapper's empty-stem retention.
func TestSnowballArabicRules(t *testing.T) {
	provider := lookupSnowballLanguage("arabic")
	if provider == nil {
		t.Fatal("missing released Arabic provider")
	}
	for _, tc := range [][2]string{
		{"كتاب", "كتاب"}, {"الكتاب", "كتاب"}, {"بالكتاب", "كتاب"}, {"للكتاب", "كتاب"}, {"وكتاب", "كتاب"}, {"فكتاب", "كتاب"}, {"مدرسة", "مدرس"}, {"المدرسة", "مدرس"}, {"المدارس", "مدارس"},
		{"كتبت", "كتب"}, {"كتبتما", "كتب"}, {"كتبوا", "كتب"}, {"كتبتم", "كتب"}, {"كتابه", "كتاب"}, {"كتابها", "كتاب"}, {"كتابهما", "كتاب"}, {"كتابنا", "كتاب"}, {"كتابي", "كتاب"}, {"كاتبون", "كاتب"}, {"كاتبان", "كاتب"}, {"كاتبين", "كاتب"}, {"يكتبون", "يكتب"}, {"سيكتب", "يكتب"}, {"يستعمل", "استعمل"},
		{"كتب", "كتب"}, {"كتي", "كت"}, {"كت", "كت"}, {"كتبا", "كتب"}, {"كبا", "كبا"}, {"كتبون", "كتبون"}, {"كتاب!", "كتاب!"}, {"الكتاب!", "كتاب!"}, {"والكتاب", "والكتاب"}, {"ال", "ال"}, {"الاب", "الاب"}, {"الكتب", "كتب"}, {"بالاب", "بالاب"}, {"بالكتب", "كتب"},
		{"كِتَابٌ", "كتاب"}, {"كـتاب", "كتاب"}, {"أمل", "امل"}, {"مسؤول", "مسوول"}, {"قارئ", "قارء"}, {"أ", "ء"}, {"إ", "ء"}, {"آ", "ء"}, {"ؤ", "ء"}, {"ئ", "ء"}, {"فتى", "فتي"}, {"على", "علي"},
		{"في", "في"}, {"من", "من"}, {"هذا", "هذا"}, {"rats", "rats"}, {"only", "only"}, {"١٢٣", "123"}, {"۱۲۳", "۱۲۳"}, {"کتاب", "کتاب"}, {"\ufe8d", "ا"}, {"\ufefb", "لا"}, {"\u0653", "\u0653"}, {"\u064e", ""}, {"\u0640", ""}, {"", ""},
	} {
		if got := provider.stem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	t.Run("released suffix guards", func(t *testing.T) {
		for _, family := range []struct {
			name  string
			apply func(*arabicState) bool
			rules []arabicAffix
		}{
			{"verb1", (*arabicState).verb1, []arabicAffix{{"ه", 4}, {"ك", 4}, {"ني", 5}, {"نا", 5}, {"ها", 5}, {"هم", 5}, {"هن", 5}, {"كم", 5}, {"كن", 5}, {"هما", 6}, {"كما", 6}, {"كمو", 6}}},
			{"verb2a", (*arabicState).verb2a, []arabicAffix{{"ت", 4}, {"ا", 4}, {"ن", 4}, {"ي", 4}, {"نا", 5}, {"تا", 5}, {"تن", 5}, {"ان", 6}, {"ون", 6}, {"ين", 6}, {"تما", 6}}},
			{"verb2c", (*arabicState).verb2c, []arabicAffix{{"و", 4}, {"تمو", 6}}},
			{"noun2", (*arabicState).noun2, []arabicAffix{{"ا", 5}, {"ي", 5}, {"و", 5}, {"ت", 4}}},
		} {
			for _, rule := range family.rules {
				for _, delta := range []int{-1, 0, 1} {
					padding := rule.minimum + delta - len([]rune(rule.text))
					input := strings.Repeat("ز", padding) + rule.text
					s := arabicState{word: []rune(input), cursor: rule.minimum + delta}
					got := family.apply(&s)
					want := input
					if delta >= 0 {
						want = strings.Repeat("ز", padding)
					}
					if got != (delta >= 0) || string(s.word) != want {
						t.Errorf("%s %q guard %d: %v %q, want %q", family.name, rule.text, delta, got, string(s.word), want)
					}
				}
			}
		}
	})
	t.Run("noun suffix guards", func(t *testing.T) {
		for _, rule := range []arabicAffix{{"ي", 4}, {"ك", 4}, {"ه", 4}, {"نا", 5}, {"كم", 5}, {"ها", 5}, {"هن", 5}, {"هم", 5}, {"كما", 6}, {"هما", 6}, {"ن", 6}, {"ا", 5}, {"ي", 5}, {"و", 5}, {"ات", 5}, {"ت", 4}, {"ة", 4}, {"ي", 3}} {
			for _, delta := range []int{-1, 0, 1} {
				padding := rule.minimum + delta - len([]rune(rule.text))
				input := strings.Repeat("ز", padding) + rule.text
				s := arabicState{word: []rune(input), cursor: rule.minimum + delta}
				got := s.suffix(rule)
				want := input
				if delta >= 0 {
					want = strings.Repeat("ز", padding)
				}
				if got != (delta >= 0) || string(s.word) != want {
					t.Errorf("noun %q guard %d: %q, want %q", rule.text, delta, string(s.word), want)
				}
			}
		}
		// The longest table entry owns its guard, even when a shorter suffix
		// would satisfy its own guard.
		s := arabicState{word: []rune("ززكما"), cursor: 5}
		if s.verb1() || string(s.word) != "ززكما" {
			t.Fatal("failed longest-match guard retried a shorter suffix")
		}
	})
	t.Run("prefix guard edges", func(t *testing.T) {
		for _, rule := range []struct {
			from, to string
			minimum  int
		}{
			{"أأ", "أ", 4}, {"أآ", "آ", 4}, {"أؤ", "أ", 4}, {"أا", "ا", 4}, {"أإ", "إ", 4},
			{"بال", "", 6}, {"كال", "", 6}, {"لل", "", 5}, {"ال", "", 5},
			{"ب", "", 4}, {"بب", "ب", 4}, {"كك", "ك", 4},
			{"سي", "ي", 5}, {"ست", "ت", 5}, {"سن", "ن", 5}, {"سأ", "أ", 5},
			{"يست", "است", 5}, {"نست", "است", 5}, {"تست", "است", 5},
		} {
			for _, delta := range []int{-1, 0, 1} {
				padding := rule.minimum + delta - len([]rune(rule.from))
				input := rule.from + strings.Repeat("ز", padding)
				s := arabicState{word: []rune(input)}
				got := s.prefix(rule.from, rule.minimum, rule.to)
				want := input
				if delta >= 0 {
					want = rule.to + strings.Repeat("ز", padding)
				}
				if got != (delta >= 0) || string(s.word) != want {
					t.Errorf("prefix %q guard %d: %q, want %q", rule.from, delta, string(s.word), want)
				}
			}
		}
	})
	t.Run("cursor and mutation alternatives", func(t *testing.T) {
		for _, tc := range [][2]string{
			{"زهما", "زهم"},       // failed long pronoun proceeds to verb2a
			{"ززززهنه", "زززز"},   // repeated verb pronoun stripping
			{"الزززن", "ززز"},     // noun ن mutates even though following noun2 fails
			{"سيستعمل", "يستعمل"}, // future rewrite cursor skips inserted ي
			{"أأكتاب", "اكتاب"},   // prefix1 cursor skips its replacement
			{"باكتاب", "باكتاب"},  // با success blocks shorter ب
		} {
			if got := provider.stem(tc[0]); got != tc[1] {
				t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
			}
		}
	})
	t.Run("normalization tables", func(t *testing.T) {
		for _, group := range []struct{ first, last, want rune }{
			{0xfe80, 0xfe80, 'ء'}, {0xfe81, 0xfe82, 'آ'}, {0xfe83, 0xfe84, 'أ'},
			{0xfe85, 0xfe86, 'ؤ'}, {0xfe87, 0xfe88, 'إ'}, {0xfe89, 0xfe8c, 'ئ'},
			{0xfe8d, 0xfe8e, 'ا'}, {0xfe8f, 0xfe92, 'ب'}, {0xfe93, 0xfe94, 'ة'},
			{0xfe95, 0xfe98, 'ت'}, {0xfe99, 0xfe9c, 'ث'}, {0xfe9d, 0xfea0, 'ج'},
			{0xfea1, 0xfea4, 'ح'}, {0xfea5, 0xfea8, 'خ'}, {0xfea9, 0xfeaa, 'د'},
			{0xfeab, 0xfeac, 'ذ'}, {0xfead, 0xfeae, 'ر'}, {0xfeaf, 0xfeb0, 'ز'},
			{0xfeb1, 0xfeb4, 'س'}, {0xfeb5, 0xfeb8, 'ش'}, {0xfeb9, 0xfebc, 'ص'},
			{0xfebd, 0xfec0, 'ض'}, {0xfec1, 0xfec4, 'ط'}, {0xfec5, 0xfec8, 'ظ'},
			{0xfec9, 0xfecc, 'ع'}, {0xfecd, 0xfed0, 'غ'}, {0xfed1, 0xfed4, 'ف'},
			{0xfed5, 0xfed8, 'ق'}, {0xfed9, 0xfedc, 'ك'}, {0xfedd, 0xfee0, 'ل'},
			{0xfee1, 0xfee4, 'م'}, {0xfee5, 0xfee8, 'ن'}, {0xfee9, 0xfeec, 'ه'},
			{0xfeed, 0xfeee, 'و'}, {0xfeef, 0xfef0, 'ى'}, {0xfef1, 0xfef4, 'ي'},
		} {
			for r := group.first; r <= group.last; r++ {
				s := arabicState{word: []rune{r}}
				s.normalizePre()
				if string(s.word) != string(group.want) {
					t.Errorf("presentation U+%04X: %q", r, string(s.word))
				}
			}
		}
		for _, tc := range [][2]string{
			{"\ufef5\ufef6", "لآلآ"}, {"\ufef7\ufef8", "لألأ"}, {"\ufef9\ufefa", "لإلإ"}, {"\ufefb\ufefc", "لالا"},
			{"٠١٢٣٤٥٦٧٨٩", "0123456789"}, {"ـًٌٍَُِّْ", ""}, {"ٓ۱۲۳کتاب", "ٓ۱۲۳کتاب"},
		} {
			s := arabicState{word: make([]rune, 0, len(tc[0]))}
			s.word = append(s.word, []rune(tc[0])...)
			s.normalizePre()
			if string(s.word) != tc[1] {
				t.Errorf("normalize %q: %q, want %q", tc[0], string(s.word), tc[1])
			}
		}
	})
}
