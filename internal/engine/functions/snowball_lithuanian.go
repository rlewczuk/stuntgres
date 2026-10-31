// Lithuanian Snowball rules from PostgreSQL REL_18_6, synchronized to
// snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// Copyright and BSD-3-Clause terms: snowball_LICENSE.txt.
package functions

import (
	"strings"
	"unicode/utf8"
)

// Mark once on the original word; later rewrites must not move this boundary.
func lithuanianRegion(word string) int {
	start := 0
	if strings.HasPrefix(word, "a") && utf8.RuneCountInString(word) > 6 {
		start = 1
	}
	vowel := false
	for offset, r := range word[start:] {
		isVowel := strings.ContainsRune("aeiyouąęįųėū", r)
		if vowel && !isVowel {
			return start + offset + utf8.RuneLen(r)
		}
		vowel = isVowel
	}
	return len(word)
}

// Snowball's setlimit restricts candidates BEFORE longest-match selection.
// A longer suffix crossing p1 cannot hide a shorter suffix inside the region.
func lithuanianSuffix(word string, p1 int, suffixes []string) string {
	best := ""
	for _, suffix := range suffixes {
		if len(suffix) > len(best) && len(word)-len(suffix) >= p1 && strings.HasSuffix(word, suffix) {
			best = suffix
		}
	}
	return best
}

func lithuanianConflicts(word string) string {
	best, replacement := "", ""
	for _, rule := range lithuanianConflictRules {
		if len(rule[0]) > len(best) && strings.HasSuffix(word, rule[0]) {
			best, replacement = rule[0], rule[1]
		}
	}
	if best != "" {
		return word[:len(word)-len(best)] + replacement
	}
	return word
}

func lithuanianCHDZ(word string) string {
	if strings.HasSuffix(word, "č") {
		return word[:len(word)-len("č")] + "t"
	}
	if strings.HasSuffix(word, "dž") {
		return word[:len(word)-len("dž")] + "d"
	}
	return word
}

func lithuanianStem(word string) string {
	p1 := lithuanianRegion(word)
	word = lithuanianConflicts(word)
	word = word[:len(word)-len(lithuanianSuffix(word, p1, lithuanianStep1[:]))]
	word = lithuanianCHDZ(word)
	for {
		suffix := lithuanianSuffix(word, p1, lithuanianStep2[:])
		if suffix == "" {
			break
		}
		word = word[:len(word)-len(suffix)]
	}
	word = lithuanianCHDZ(word)
	if strings.HasSuffix(word, "gd") {
		word = word[:len(word)-1]
	}
	return word
}

var lithuanianStep1 = [...]string{
	"as", "ias", "is", "ys", "o", "io", "ui", "iui",
	"ą", "ią", "į", "u", "iu", "e", "yje", "y",
	"au", "i", "an", "ai", "iai", "ų", "ių", "ams",
	"am", "iams", "iam", "us", "ius", "ais", "iais", "uose",
	"iuose", "uos", "iuos", "uosna", "iuosna", "ysna", "asis", "aisi",
	"osi", "ųsi", "uisi", "ąsi", "usi", "esi", "uo", "a",
	"ia", "os", "ios", "oj", "oje", "ioje", "osna", "iosna",
	"om", "oms", "ioms", "omis", "iomis", "ose", "iose", "on",
	"ion", "ė", "ės", "ei", "ę", "ėj", "ėje", "ėms",
	"es", "ėmis", "ėse", "ėsna", "ėn", "aus", "iaus", "umi",
	"iumi", "uje", "iuje", "iau", "ūs", "ums", "umis", "un",
	"iun", "ies", "ens", "enio", "ers", "eniui", "eriai", "enį",
	"erį", "imi", "eniu", "erimi", "eria", "enyje", "eryje", "ie",
	"enie", "erie", "enys", "erys", "erų", "ims", "enims", "erims",
	"enis", "imis", "enimis", "yse", "enyse", "eryse", "iem", "iems",
	"ame", "iame", "uosi", "iuosi", "iesi", "asi", "iasi", "amės",
	"iamės", "at", "ate", "iat", "iate", "atės", "iatės", "isi",
	"im", "imės", "it", "ite", "ome", "ot", "ote", "ėjo",
	"ėjosi", "otės", "eisi", "ėsi", "ėm", "ėme", "ėmės", "ėt",
	"ėte", "ėtės", "ausi", "omės", "siu", "siuosi", "si", "siesi",
	"s", "ysis", "sim", "sime", "sit", "site", "čiau", "čiausi",
	"tum", "tumei", "tumeis", "tumeisi", "tųsi", "tumėm", "tumėme", "tumėmės",
	"tute", "tumėt", "tumėte", "tumėtės", "k", "ki", "kimės", "uoti",
	"iuoti", "auti", "iauti", "oti", "ioti", "ėti", "yti", "inti",
	"inėti", "enti", "telėti", "terėti", "ti", "ąs", "iąs", "įs",
	"tųs", "simės", "sitės", "kite",
}

var lithuanianStep2 = [...]string{
	"ing", "išk", "ėt", "ot", "uot", "iuot", "yt", "iuk",
	"iul", "ėl", "yl", "učiuk", "uliuk", "utėait", "ok", "iok",
	"sv", "šv", "zgan", "op", "iop", "ain", "ykšt", "ykšč",
	"esn", "aus", "iaus", "ias", "oj", "ioj", "aj", "iaj",
	"ąj", "iąj", "uoj", "iuoj", "iej", "ųj", "iųj", "ies",
	"uos", "iuos", "ais", "iais", "os", "ios", "ąs", "iąs",
	"dav", "ant", "iant", "int", "ėj", "ę", "ėję", "ęs",
	"siant", "dam", "auj", "jam", "iau", "am",
}

var lithuanianConflictRules = [...][2]string{
	{"aite", "aitė"},
	{"aitės", "aitė"},
	{"uotės", "uotė"},
	{"uote", "uotė"},
	{"ėjime", "ėjimas"},
	{"esiu", "esys"},
	{"asius", "asys"},
	{"avime", "avimas"},
	{"ojime", "ojimas"},
	{"okatės", "okatė"},
	{"okate", "okatė"},
}
