package functions

import "strings"

// Nepali rules are a direct string-slice port of PostgreSQL REL_18_6
// src/backend/snowball/libstemmer/stem_UTF_8_nepali.c (Snowball 2.2.0).
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_nepali.c
// Snowball's BSD license is retained in snowball_LICENSE.txt.
var nepaliCategory1 = [...]string{
	"की", "लाइ", "ले", "लाई", "कै", "सँगै", "मै", "को", "सँग", "संग",
	"मार्फत", "रत", "का", "मा", "द्वारा", "कि", "पछि",
}

var nepaliCategory3 = [...]string{
	"ेकी", "एकी", "इएकी", "िएकी", "देखी", "थी", "दी", "छु", "ेछु", "नेछु", "एछु",
	"नु", "हरु", "हरू", "छे", "थे", "ने", "ेकै", "नेकै", "एकै", "दै", "इदै", "िदै",
	"ेको", "नेको", "एको", "इएको", "िएको", "दो", "इदो", "िदो", "यो", "इयो", "थ्यो",
	"भयो", "ियो", "थियो", "दियो", "छौ", "इछौ", "ेछौ", "नेछौ", "एछौ", "िछौ", "यौ",
	"छ्यौ", "थ्यौ", "थियौ", "छन्", "इछन्", "ेछन्", "नेछन्", "एछन्", "िछन्", "लान्",
	"छिन्", "थिन्", "पर्", "इस्", "थिइस्", "छेस्", "होस्", "छस्", "इछस्", "ेछस्",
	"नेछस्", "एछस्", "िछस्", "िस्", "थिस्", "थिए", "छ", "इछ", "ेछ", "नेछ", "हुनेछ",
	"हुन्छ", "इन्छ", "िन्छ", "एछ", "िछ", "ेका", "नेका", "एका", "इएका", "िएका", "दा",
	"इदा", "िदा", "देखि", "माथि",
}

// All suffixes are complete UTF8 strings, so byte boundaries are sufficient.
// Selecting explicitly by length preserves Snowball's longest among match.
func nepaliLongestSuffix(word string, suffixes []string) string {
	longest := ""
	for _, suffix := range suffixes {
		if len(suffix) > len(longest) && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	return longest
}

func nepaliStem(word string) string {
	if suffix := nepaliLongestSuffix(word, nepaliCategory1[:]); suffix != "" {
		prefix := word[:len(word)-len(suffix)]
		blocked := false
		switch suffix {
		case "की", "कै", "को", "का", "कि":
			blocked = strings.HasSuffix(prefix, "ए") || strings.HasSuffix(prefix, "े")
		}
		if !blocked {
			word = prefix
		}
	}
	for {
		// Category two removes only the mark, not its lookbehind context.
		// Its deletion survives even when category three subsequently fails.
		if strings.HasSuffix(word, "ँ") || strings.HasSuffix(word, "ं") {
			prefix := word[:len(word)-len("ँ")]
			if strings.HasSuffix(prefix, "यौ") || strings.HasSuffix(prefix, "छौ") || strings.HasSuffix(prefix, "नौ") || strings.HasSuffix(prefix, "थे") {
				word = prefix
			}
		} else if strings.HasSuffix(word, "त्रै") {
			word = word[:len(word)-len("ै")]
		}
		suffix := nepaliLongestSuffix(word, nepaliCategory3[:])
		if suffix == "" {
			return word
		}
		word = word[:len(word)-len(suffix)]
		// Each category-three success shortens the bounded input; category
		// one is deliberately never revisited by this repeat.
	}
}
