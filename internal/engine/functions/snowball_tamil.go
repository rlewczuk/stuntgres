// Tamil Snowball rules from PostgreSQL REL_18_6 (Snowball 2.2.0), synchronized
// to snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// Copyright and BSD-3-Clause terms: snowball_LICENSE.txt.
package functions

import (
	"strings"
	"unicode/utf8"
)

// All offsets are UTF8 byte boundaries. Length gates count codepoints, not
// graphemes. The case flag is invocation-local and survives subsequent phases.
type tamilWord struct {
	word string
	caseRemoved bool
}

func tamilAmong(word string, alternatives []string) string {
	best := ""
	for _, s := range alternatives {
		if len(s) > len(best) && strings.HasSuffix(word, s) {
			best = s
		}
	}
	return best
}

func tamilLastIn(word, set string) bool {
	r, size := utf8.DecodeLastRuneInString(word)
	return size != 0 && strings.ContainsRune(set, r)
}

const tamilSigns = "ாிீெேுூை"
const tamilHard = "கசடதபற"
const tamilSoft = "யரலவழளஞணநமன"

func (t *tamilWord) replace(suffix, replacement string) {
	t.word = t.word[:len(t.word)-len(suffix)] + replacement
}

func (t *tamilWord) long() bool { return utf8.RuneCountInString(t.word) > 4 }

func (t *tamilWord) fixEnding() bool {
	if utf8.RuneCountInString(t.word) <= 3 {
		return false
	}
	s := tamilAmong(t.word, []string{"ந்", "ந்த", "ந்த்", "ய்", "ட்ப்", "ட்க்", "ன்ற்", "ற்க்", "ட்ட்", "த்த்", "ுக்", "ுக்க்", "வ", "ய", "வ்", "னு", "ங்"})
	prefix := t.word[:len(t.word)-len(s)]
	replacement, accepted := "", s != ""
	switch s {
	case "ய்":
		accepted = tamilLastIn(prefix, "ைிீ")
	case "ட்ப்", "ட்க்":
		replacement = "ள்"
	case "ன்ற்", "ற்க்":
		replacement = "ல்"
	case "ட்ட்":
		replacement = "டு"
	case "த்த்":
		accepted = t.caseRemoved && !strings.HasSuffix(prefix, "ை")
		replacement = "ம்"
	case "ுக்", "ுக்க்":
		replacement = "்"
	case "னு":
		accepted = !tamilLastIn(prefix, tamilSigns)
	case "ங்":
		// The among consumes the preceding sign, but slice boundaries still
		// surround only ங். An empty match repairs it to ம்.
		if !tamilLastIn(prefix, "ை்") {
			replacement = "ம்"
		}
	}
	if accepted {
		t.replace(s, replacement)
		return true
	}
	if !strings.HasSuffix(t.word, "்") {
		return false
	}
	prefix = t.word[:len(t.word)-len("்")]
	r, size := utf8.DecodeLastRuneInString(prefix)
	if size != 0 && strings.ContainsRune(tamilHard, r) {
		start := len(prefix)-size
		before := prefix[:start]
		if strings.HasSuffix(before, "்") {
			previous := before[:len(before)-len("்")]
			if tamilLastIn(previous, tamilHard) {
				_, n := utf8.DecodeLastRuneInString(previous)
				start = len(previous)-n
			}
		}
		t.word = t.word[:start]
		return true
	}
	if size != 0 && strings.ContainsRune(tamilSoft, r) {
		before := prefix[:len(prefix)-size]
		if strings.HasSuffix(before, "்") {
			t.word = before
			return true
		}
	}
	if tamilLastIn(prefix, tamilSigns+"்") {
		t.word = prefix
		return true
	}
	return false
}

func (t *tamilWord) fixEndings() {
	for t.fixEnding() {
	}
}

func (t *tamilWord) prefix(question bool) {
	first, n := utf8.DecodeRuneInString(t.word)
	if n == 0 || (question && first != 'எ') || (!question && !strings.ContainsRune("அஇஉ", first)) {
		return
	}
	r, size := utf8.DecodeRuneInString(t.word[n:])
	if size == 0 || !strings.ContainsRune("கசதவநபமயஙஞ", r) || !strings.HasPrefix(t.word[n+size:], "்") {
		return
	}
	t.word = t.word[n+size+len("்"):]
	for _, rule := range [][2]string{{"வோ", "ஓ"}, {"வொ", "ஒ"}, {"வு", "உ"}, {"வூ", "ஊ"}} {
		if strings.HasPrefix(t.word, rule[0]) {
			t.word = rule[1]+t.word[len(rule[0]):]
			break
		}
	}
}

func (t *tamilWord) question() {
	if !t.long() { return }
	if s := tamilAmong(t.word, []string{"ோ", "ே", "ா"}); s != "" {
		t.replace(s, "்")
	}
	// This runs even when the optional question suffix did not match.
	t.fixEndings()
}

func (t *tamilWord) um() {
	if !t.long() || !strings.HasSuffix(t.word, "ும்") { return }
	t.replace("ும்", "்")
	t.fixEnding()
}

var tamilCommon = [...]string{
	"ுடன்", "ில்லை", "ிடம்", "ின்றி", "ாகி", "ாகிய", "ென்று", "ுள்ள", "ுடைய", "ுடை", "ெனும்", "ென",
	"ல்ல", "படு", "பட்ட", "பட்டு", "பட்டது", "பட்டண", "குரிய", "பற்றி", "விடு", "விட்டு", "படிதான", "படி", "தான", "ெல்லாம்",
}

func (t *tamilWord) common() {
	if !t.long() { return }
	s := tamilAmong(t.word, tamilCommon[:])
	if s == "" { return }
	replacement := ""
	switch s {
	case "ுடன்", "ில்லை", "ிடம்", "ின்றி", "ாகி", "ாகிய", "ென்று", "ுள்ள", "ுடைய", "ுடை", "ெனும்", "ென":
		replacement = "்"
	case "ல்ல":
		if tamilLastIn(t.word[:len(t.word)-len(s)], tamilSigns) { return }
		replacement = "்"
	}
	t.replace(s, replacement)
	t.fixEndings()
}

var tamilCases = [...]string{
	"னை", "ொடு", "ோடு", "ில்", "ிற்", "ின்று", "ிருந்து", "விட", "ால்", "ுடை", "ாமல்", "ுள்", "ின்", "ிடம்", "ல்", "கண்", "முன்", "மேல்", "மேற்", "கீழ்", "து", "ீ",
}

func (t *tamilWord) cases() {
	t.caseRemoved = false
	if !t.long() { return }
	s := tamilAmong(t.word, tamilCases[:])
	prefix := t.word[:len(t.word)-len(s)]
	accepted, replacement := s != "", "்"
	switch s {
	case "னை", "கண்", "முன்", "மேல்", "மேற்", "கீழ்": replacement = ""
	case "ின்": accepted = !strings.HasSuffix(prefix, "ம")
	case "ிடம்": accepted = utf8.RuneCountInString(t.word) >= 7
	case "ல்": accepted = !tamilLastIn(prefix, tamilSigns)
	case "து": accepted = !tamilLastIn(prefix, tamilSigns); replacement = ""
	case "ீ": replacement = "ி"
	}
	if accepted {
		t.replace(s, replacement)
	} else {
		if !strings.HasSuffix(t.word, "ை") { return }
		prefix = t.word[:len(t.word)-len("ை")]
		if tamilLastIn(prefix, tamilHard) {
			_, n := utf8.DecodeLastRuneInString(prefix)
			if !strings.HasSuffix(prefix[:len(prefix)-n], "்") { return }
		}
		t.replace("ை", "்")
	}
	t.caseRemoved = true
	if strings.HasSuffix(t.word, "ின்") { t.replace("ின்", "்") }
	t.fixEndings()
}

func (t *tamilWord) plural() {
	s := tamilAmong(t.word, []string{"ுங்கள்", "ற்கள்", "ட்கள்", "கள்"})
	switch s {
	case "ுங்கள்":
		replacement := "்"
		if tamilLastIn(t.word[:len(t.word)-len(s)], tamilHard) { replacement = "ுங்" }
		t.replace(s, replacement)
	case "ற்கள்": t.replace(s, "ல்")
	case "ட்கள்": t.replace(s, "ள்")
	case "கள்": t.replace(s, "")
	}
}

func (t *tamilWord) command() {
	if !t.long() { return }
	if s := tamilAmong(t.word, []string{"பி", "வி"}); s != "" { t.replace(s, "") }
}

var tamilTenses = [...]string{
	"கொண்டிர்", "படு", "மார்", "மின்", "னன்", "னான்", "னாள்", "னார்", "னள்", "வள்", "னர்", "வர்", "ன", "ப", "க", "த", "ய", "பன்", "பள்", "பர்", "ிற்று", "பம்", "னம்", "தும்", "றும்", "கும்", "னென்", "னை", "வை",
	"வன்", "து", "ான்", "ாள்", "ார்", "ேன்", "ா", "ாம்", "ெம்", "ேம்", "ோம்", "டும்", "ாய்", "னிர்", "ீர்", "ீயர்", "கு",
}
var tamilSecondaryTenses = [...]string{"ாநின்ற", "ாநின்ற்", "கின்ற", "கின்ற்", "கிற", "கிற்"}

func (t *tamilWord) tense() bool {
	if !t.long() { return false }
	s := tamilAmong(t.word, tamilTenses[:])
	prefix := t.word[:len(t.word)-len(s)]
	accepted, replacement := s != "", ""
	switch s {
	case "வன்": accepted = !tamilLastIn(prefix, "அஆஇஈஉஊஎஏஐஒஓஔ")
	case "து": accepted = !tamilLastIn(prefix, tamilSigns)
	case "ான்": accepted = !strings.HasSuffix(prefix, "ச"); replacement = "்"
	case "ாள்", "ார்", "ேன்", "ா", "ாம்", "ெம்", "ேம்", "ோம்", "டும்", "ாய்", "னிர்", "ீர்", "ீயர்": replacement = "்"
	case "கு": accepted = strings.HasSuffix(prefix, "்")
	}
	if accepted { t.replace(s, replacement) }
	secondary := tamilAmong(t.word, tamilSecondaryTenses[:])
	if secondary != "" { t.replace(secondary, "") }
	t.fixEndings()
	return accepted || secondary != ""
}

func tamilStem(word string) string {
	t := tamilWord{word: word}
	t.fixEnding() // Deliberately only once before the main length gate.
	if !t.long() { return t.word }
	t.prefix(true)
	t.prefix(false)
	t.question()
	t.um()
	t.common()
	t.cases()
	t.plural()
	t.command()
	for t.tense() {
	}
	return t.word
}
