package functions

import (
	"strings"
	"unicode/utf8"
)

// Irish morphology follows PostgreSQL REL_18_6's Snowball irish.sbl
// (upstream d19326ac6c1b9a417fc872f7c2f845265a5e9ece). See snowball_LICENSE.txt.
// Offsets are UTF-8 byte boundaries, marked after the single initial mutation
// and retained across all three suffix stages. Tables and providers are immutable.
type irishRule struct {
	suffix      string
	replacement string
	region      uint8 // 0: unconditional, 1: R1, 2: R2, 3: RV
}

var irishInitialRules = [...]irishRule{
	{"h-", "", 0}, {"n-", "", 0}, {"t-", "", 0},
	{"d'", "", 0}, {"d'fh", "f", 0}, {"m'", "", 0}, {"b'", "", 0},
	{"sh", "s", 0}, {"mb", "b", 0}, {"gc", "c", 0}, {"nd", "d", 0},
	{"bhf", "f", 0}, {"ng", "g", 0}, {"bp", "p", 0}, {"ts", "s", 0}, {"dt", "t", 0},
	{"bh", "b", 0}, {"ch", "c", 0}, {"dh", "d", 0}, {"fh", "f", 0},
	{"gh", "g", 0}, {"mh", "m", 0}, {"ph", "p", 0}, {"th", "t", 0},
}

var irishNounRules = [...]irishRule{
	{"amh", "", 1}, {"eamh", "", 1}, {"abh", "", 1}, {"eabh", "", 1},
	{"aibh", "", 1}, {"ibh", "", 1}, {"aimh", "", 1}, {"imh", "", 1},
	{"aíocht", "", 1}, {"íocht", "", 1}, {"aíochta", "", 1}, {"íochta", "", 1},
	{"ire", "", 2}, {"irí", "", 2}, {"aire", "", 2}, {"airí", "", 2},
}

var irishDerivationRules = [...]irishRule{
	{"acht", "", 2}, {"eacht", "", 2}, {"ach", "", 2}, {"each", "", 2},
	{"eachtúil", "", 2}, {"eachta", "", 2}, {"achtúil", "", 2}, {"achta", "", 2},
	{"arcacht", "arc", 0}, {"arcachtaí", "arc", 0}, {"arcachta", "arc", 0},
	{"gineach", "gin", 0}, {"gineas", "gin", 0}, {"ginis", "gin", 0},
	{"grafaíoch", "graf", 0}, {"grafaíocht", "graf", 0}, {"grafaíochta", "graf", 0}, {"grafaíochtaí", "graf", 0},
	{"paite", "paite", 0}, {"patach", "paite", 0}, {"pataigh", "paite", 0}, {"patacha", "paite", 0},
	{"óideach", "óid", 0}, {"óideacha", "óid", 0}, {"óidigh", "óid", 0},
}

var irishVerbRules = [...]irishRule{
	{"imid", "", 3}, {"aimid", "", 3}, {"ímid", "", 3}, {"aímid", "", 3},
	{"faidh", "", 3}, {"fidh", "", 3}, {"ain", "", 1}, {"eadh", "", 1},
	{"adh", "", 1}, {"áil", "", 1}, {"tear", "", 1}, {"tar", "", 1},
}

func irishVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'á', 'é', 'í', 'ó', 'ú':
		return true
	}
	return false
}

func irishRegions(word string) (rv, r1, r2 int) {
	rv, r1, r2 = len(word), len(word), len(word)
	stage := 0
	for i, r := range word {
		vowel := irishVowel(r)
		if (stage == 0 || stage == 2) && vowel || (stage == 1 || stage == 3) && !vowel {
			end := i + utf8.RuneLen(r)
			switch stage {
			case 0:
				rv = end
			case 1:
				r1 = end
			case 3:
				r2 = end
			}
			stage++
			if stage == 4 {
				break
			}
		}
	}
	return
}

func irishSuffix(word string, rules []irishRule, rv, r1, r2 int) string {
	var longest *irishRule
	for i := range rules {
		rule := &rules[i]
		if (longest == nil || len(rule.suffix) > len(longest.suffix)) && strings.HasSuffix(word, rule.suffix) {
			longest = rule
		}
	}
	if longest == nil {
		return word
	}
	start := len(word) - len(longest.suffix)
	boundary := 0
	switch longest.region {
	case 1:
		boundary = r1
	case 2:
		boundary = r2
	case 3:
		boundary = rv
	}
	// Select globally before testing the region: never retry a shorter match.
	if start < boundary || longest.suffix == longest.replacement {
		return word
	}
	return word[:start] + longest.replacement
}

func irishStem(word string) string {
	var initial *irishRule
	for i := range irishInitialRules {
		rule := &irishInitialRules[i]
		if (initial == nil || len(rule.suffix) > len(initial.suffix)) && strings.HasPrefix(word, rule.suffix) {
			initial = rule
		}
	}
	if initial != nil {
		word = initial.replacement + word[len(initial.suffix):]
	}
	rv, r1, r2 := irishRegions(word)
	word = irishSuffix(word, irishNounRules[:], rv, r1, r2)
	word = irishSuffix(word, irishDerivationRules[:], rv, r1, r2)
	return irishSuffix(word, irishVerbRules[:], rv, r1, r2)
}
