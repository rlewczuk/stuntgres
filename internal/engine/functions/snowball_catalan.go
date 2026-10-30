package functions

import (
	"strings"
	"unicode/utf8"
)

// Port of PostgreSQL REL_18_6 stem_UTF_8_catalan.c, Snowball revision
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece. See snowball_LICENSE.txt.
func catalanVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'á', 'à', 'é', 'è', 'í', 'ï', 'ó', 'ò', 'ú', 'ü':
		return true
	}
	return false
}

// Marks belong to the original UTF8 input; subsequent edits never move them.
func catalanRegions(input string) (p1, p2 int) {
	p1, p2 = len(input), len(input)
	previousVowel := false
	marked := false
	for i, r := range input {
		vowel := catalanVowel(r)
		if previousVowel && !vowel {
			if !marked {
				p1 = i + utf8.RuneLen(r)
				marked = true
			} else {
				p2 = i + utf8.RuneLen(r)
				break
			}
		}
		previousVowel = vowel
	}
	return
}

// Terminal deletion borrows input. Replacement uses one invocation-local buffer,
// reused through cleaning; no scratch or mutable cursor escapes this invocation.
type catalanWord struct {
	input  string
	buffer []byte
}

func (w *catalanWord) length() int {
	if w.buffer != nil {
		return len(w.buffer)
	}
	return len(w.input)
}

func (w *catalanWord) hasSuffix(s string) bool {
	if w.buffer == nil {
		return strings.HasSuffix(w.input, s)
	}
	start := len(w.buffer) - len(s)
	if start < 0 {
		return false
	}
	for i := range len(s) {
		if w.buffer[start+i] != s[i] {
			return false
		}
	}
	return true
}

func (w *catalanWord) replace(rule catalanRule, regions [3]int) bool {
	start := w.length() - len(rule.suffix)
	if rule.suffix == "" || start < regions[rule.region] {
		return false
	}
	if rule.suffix == rule.replacement {
		return true
	}
	if w.buffer == nil && rule.replacement == "" {
		w.input = w.input[:start]
		return true
	}
	if w.buffer == nil {
		w.buffer = []byte(w.input)
	}
	w.buffer = append(w.buffer[:start], rule.replacement...)
	return true
}

func (w *catalanWord) longest(suffixes []string, region int, selected catalanRule) catalanRule {
	for _, suffix := range suffixes {
		if len(suffix) > len(selected.suffix) && w.hasSuffix(suffix) {
			selected = catalanRule{suffix: suffix, region: region}
		}
	}
	return selected
}

func (w *catalanWord) standard(regions [3]int) bool {
	selected := w.longest(catalanStandardR1[:], 1, catalanRule{})
	for _, rule := range catalanStandardOther {
		if len(rule.suffix) > len(selected.suffix) && w.hasSuffix(rule.suffix) {
			selected = rule
		}
	}
	return w.replace(selected, regions)
}

func (w *catalanWord) verb(regions [3]int) bool {
	selected := w.longest(catalanVerbR1[:], 1, catalanRule{})
	if len(selected.suffix) < len("ando") && w.hasSuffix("ando") {
		selected = catalanRule{suffix: "ando", region: 2}
	}
	return w.replace(selected, regions)
}

func (w *catalanWord) residual(regions [3]int) bool {
	selected := w.longest(catalanResidual[:], 1, catalanRule{})
	if len(selected.suffix) < len("iqu") && w.hasSuffix("iqu") {
		selected = catalanRule{suffix: "iqu", region: 1, replacement: "ic"}
	}
	return w.replace(selected, regions)
}

func catalanClean(r rune) byte {
	switch r {
	case 'à', 'á':
		return 'a'
	case 'è', 'é':
		return 'e'
	case 'ì', 'í', 'ï':
		return 'i'
	case 'ò', 'ó':
		return 'o'
	case 'ú', 'ü':
		return 'u'
	case '·':
		return '.'
	}
	return 0
}

func (w *catalanWord) clean() string {
	// Locate the first real change before allocating or copying any bytes.
	first := -1
	if w.buffer == nil {
		for i, r := range w.input {
			if catalanClean(r) != 0 {
				first = i
				break
			}
		}
		if first < 0 {
			return w.input
		}
		w.buffer = []byte(w.input)
	} else {
		first = 0
	}
	out := first
	for pos := first; pos < len(w.buffer); {
		r, n := utf8.DecodeRune(w.buffer[pos:])
		if replacement := catalanClean(r); replacement != 0 {
			w.buffer[out] = replacement
			out++
		} else {
			out += copy(w.buffer[out:], w.buffer[pos:pos+n])
		}
		pos += n
	}
	return string(w.buffer[:out])
}

func catalanStem(input string) string {
	p1, p2 := catalanRegions(input)
	regions := [3]int{0, p1, p2}
	word := catalanWord{input: input}
	word.replace(word.longest(catalanPronouns[:], 1, catalanRule{}), regions)
	if !word.standard(regions) {
		word.verb(regions)
	}
	word.residual(regions)
	return word.clean()
}
