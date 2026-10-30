package functions

import (
	"strings"
	"unicode/utf8"
)

// Hindi rules are ported from PostgreSQL REL_18_6's
// src/backend/snowball/libstemmer/stem_UTF_8_hindi.c and its original hindi.sbl
// at Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece (BSD Snowball license;
// see snowball_LICENSE.txt). The first code point is protected, including from
// the conditional predecessor test. One longest valid suffix is deleted;
// failed CONSONANT predicates permit shorter matches, as in find_among_b.
var hindiSuffixes = [...]struct {
	suffix    string
	consonant bool
}{
	{"्", false},
	{"अ", false},
	{"आ", false},
	{"इ", false},
	{"ई", false},
	{"उ", false},
	{"ऊ", false},
	{"ए", false},
	{"ओ", false},
	{"एं", false},
	{"ओं", false},
	{"आं", false},
	{"उआं", false},
	{"उएं", false},
	{"उओं", false},
	{"आएं", false},
	{"आओं", false},
	{"इयां", false},
	{"इयों", false},
	{"आइयां", false},
	{"आइयों", false},
	{"आँ", false},
	{"इयाँ", false},
	{"आइयाँ", false},
	{"अताएं", false},
	{"अताओं", false},
	{"अनाएं", false},
	{"अनाओं", false},
	{"अता", false},
	{"अती", false},
	{"ईं", false},
	{"अतीं", false},
	{"अते", false},
	{"आता", false},
	{"आती", false},
	{"आतीं", false},
	{"आते", false},
	{"अना", false},
	{"अनी", false},
	{"अने", false},
	{"आना", false},
	{"आने", false},
	{"ऊंगा", false},
	{"ऊंगी", false},
	{"आऊंगा", false},
	{"आऊंगी", false},
	{"एंगे", false},
	{"एंगी", false},
	{"आएंगे", false},
	{"आएंगी", false},
	{"ओगे", false},
	{"ओगी", false},
	{"आओगे", false},
	{"आओगी", false},
	{"एगा", false},
	{"एगी", false},
	{"आएगा", false},
	{"आएगी", false},
	{"आया", false},
	{"आए", false},
	{"आई", false},
	{"आईं", false},
	{"इए", false},
	{"आओ", false},
	{"आइए", false},
	{"अकर", false},
	{"आकर", false},
	{"ा", false},
	{"ि", false},
	{"ी", false},
	{"ु", false},
	{"ू", false},
	{"े", false},
	{"ो", false},
	{"ें", false},
	{"ों", false},
	{"ां", false},
	{"ुआं", false},
	{"ुएं", false},
	{"ुओं", false},
	{"ाएं", false},
	{"ाओं", false},
	{"ियां", false},
	{"ियों", false},
	{"ाइयां", false},
	{"ाइयों", false},
	{"ाँ", false},
	{"ियाँ", false},
	{"ाइयाँ", false},
	{"ीं", false},
	{"ाता", false},
	{"ाती", false},
	{"ातीं", false},
	{"ाते", false},
	{"ाना", false},
	{"ाने", false},
	{"ूंगा", false},
	{"ूंगी", false},
	{"ाऊंगा", false},
	{"ाऊंगी", false},
	{"ेंगे", false},
	{"ेंगी", false},
	{"ाएंगे", false},
	{"ाएंगी", false},
	{"ोगे", false},
	{"ोगी", false},
	{"ाओगे", false},
	{"ाओगी", false},
	{"ेगा", false},
	{"ेगी", false},
	{"ाएगा", false},
	{"ाएगी", false},
	{"ाया", false},
	{"ाए", false},
	{"ाई", false},
	{"ाईं", false},
	{"िए", false},
	{"ाओ", false},
	{"ाइए", false},
	{"ाकर", false},
	{"ताएं", true},
	{"ताओं", true},
	{"नाएं", true},
	{"नाओं", true},
	{"ता", true},
	{"ती", true},
	{"तीं", true},
	{"ते", true},
	{"ना", true},
	{"नी", true},
	{"ने", true},
	{"कर", true},
}

func hindiConsonant(r rune) bool {
	// Exactly the released consonant group: क..ह includes the three
	// precomposed ऩ/ऱ/ऴ; nukta and क़..य़ are also explicitly included.
	return r >= 'क' && r <= 'ह' || r == '़' || r >= 'क़' && r <= 'य़'
}

func hindiStem(input string) string {
	_, protected := utf8.DecodeRuneInString(input)
	longest := 0
	for _, rule := range hindiSuffixes {
		n := len(rule.suffix)
		start := len(input) - n
		if n <= longest || start < protected || !strings.HasSuffix(input, rule.suffix) {
			continue
		}
		if rule.consonant {
			if start == protected {
				continue
			}
			r, _ := utf8.DecodeLastRuneInString(input[protected:start])
			if !hindiConsonant(r) {
				continue
			}
		}
		longest = n
	}
	return input[:len(input)-longest]
}
