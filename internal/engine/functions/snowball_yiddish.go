// Yiddish Snowball rules from PostgreSQL REL_18_6, synchronized to
// snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// Copyright and BSD-3-Clause terms: snowball_LICENSE.txt.
package functions

import "unicode/utf8"

// All offsets, including the fixed region and R1plus3, are UTF8 byte offsets.
// Scratch belongs to one invocation; unchanged words keep their original backing.
// Most edits shrink; the Tes branch can expand בראַכ's normalized בראכ to ברענג.
type yiddishWord struct {
	text string
	buf []byte
	c, p1 int
}

func (w *yiddishWord) length() int {
	if w.buf != nil { return len(w.buf) }
	return len(w.text)
}

func (w *yiddishWord) at(i int, s string) bool {
	if i < 0 || i+len(s) > w.length() { return false }
	if w.buf == nil { return w.text[i:i+len(s)] == s }
	for j := range len(s) { if w.buf[i+j] != s[j] { return false } }
	return true
}

func (w *yiddishWord) runeAt(i int) (rune, int) {
	if i >= w.length() { return 0, 0 }
	if w.buf == nil { return utf8.DecodeRuneInString(w.text[i:]) }
	return utf8.DecodeRune(w.buf[i:])
}

func (w *yiddishWord) before(i int) (rune, int) {
	if i == 0 { return 0, 0 }
	if w.buf == nil { return utf8.DecodeLastRuneInString(w.text[:i]) }
	return utf8.DecodeLastRune(w.buf[:i])
}

func (w *yiddishWord) replace(start, end int, s string) {
	if end-start == len(s) && w.at(start, s) { return }
	oldLen := w.length()
	newLen := oldLen-(end-start)+len(s)
	if w.buf == nil {
		if end == oldLen && s == "" { w.text = w.text[:start]; return }
		w.buf = make([]byte, max(oldLen, newLen))
		copy(w.buf, w.text)
	} else if newLen > cap(w.buf) {
		grown := make([]byte, newLen)
		copy(grown, w.buf)
		w.buf = grown
	} else if newLen > len(w.buf) {
		w.buf = w.buf[:newLen]
	}
	copy(w.buf[start+len(s):], w.buf[end:oldLen])
	copy(w.buf[start:], s)
	w.buf = w.buf[:newLen]
}

func (w *yiddishWord) result() string {
	if w.buf == nil { return w.text }
	return string(w.buf)
}

func yiddishVowel(r rune) bool {
	switch r { case 'א', 'ו', 'י', 'ע', 'ױ', 'ײ': return true }
	return false
}

func yiddishConsonant(r rune) bool {
	return (r >= 'א' && r <= 'ת' || r == 'װ') && !yiddishVowel(r)
}

func yiddishNiked(r rune) bool {
	return r >= 0x05b0 && r <= 0x05b9 || r == 0x05bb || r == 0x05bc || r == 0x05bf || r == 0x05c1 || r == 0x05c2
}

func (w *yiddishWord) prelude() {
	for i := 0; i < w.length(); {
		r, n := w.runeAt(i)
		old, next, excluded := "", "", ""
		switch r {
		case 'ו':
			if w.at(i, "וו") { old, next, excluded = "וו", "װ", "ּ" } else if w.at(i, "וי") { old, next, excluded = "וי", "ױ", "ִ" }
		case 'י':
			if w.at(i, "יי") { old, next, excluded = "יי", "ײ", "ִ" }
		case 'ך': old, next = "ך", "כ"
		case 'ם': old, next = "ם", "מ"
		case 'ן': old, next = "ן", "נ"
		case 'ף': old, next = "ף", "פ"
		case 'ץ': old, next = "ץ", "צ"
		}
		if old != "" && (excluded == "" || !w.at(i+len(old), excluded)) {
			w.replace(i, i+len(old), next)
			continue
		}
		i += n
	}
	for i := 0; i < w.length(); {
		r, n := w.runeAt(i)
		if yiddishNiked(r) { w.replace(i, i+n, "") } else { i += n }
	}
	w.c = 0
}

// markRegions intentionally leaves marker/p1 changes on failure, just as the
// generated C's do restores only the caller's cursor, never the edited buffer.
func (w *yiddishWord) markRegions() bool {
	w.p1 = w.length()
	start := w.c
	if w.at(start, "גע") && !w.at(start+4, "לט") && !w.at(start+4, "בנ") && start+4 != w.length() {
		w.replace(start, start+4, "GE")
		w.c = start+2
	}
	start = w.c
	prefix := ""
	for _, p := range yiddishPrefixes { if len(p) > len(prefix) && w.at(start, p) { prefix = p } }
	if prefix != "" {
		w.c += len(prefix)
		switch {
		case (w.at(w.c, "צוגנ") || w.at(w.c, "צוקט") || w.at(w.c, "צוקנ")) && w.c+8 == w.length():
		case w.at(w.c, "געבנ"):
		case w.at(w.c, "גע"):
			w.replace(w.c, w.c+4, "GE"); w.c += 2
		case w.at(w.c, "צו"):
			w.replace(w.c, w.c+4, "TSU"); w.c += 3
		default: w.c = start
		}
	}
	x := w.c
	for range 3 {
		_, n := w.runeAt(x)
		if n == 0 { return false }
		x += n
	}
	for _, cluster := range [...]string{"שפר", "שטר", "שטש", "\u05d3\u05d6\u05e9"} {
		if w.at(w.c, cluster) { w.c += len(cluster); break }
	}
	start = w.c
	for range 3 {
		r, n := w.runeAt(w.c)
		if !yiddishConsonant(r) { w.c = start; break }
		w.c += n
		if w.c-start == 6 { w.p1 = w.c; return false }
	}
	for {
		r, n := w.runeAt(w.c)
		if n == 0 { return false }
		if yiddishVowel(r) { break }
		w.c += n
	}
	for {
		r, n := w.runeAt(w.c)
		if !yiddishVowel(r) { break }
		w.c += n
	}
	w.p1 = max(w.c, x)
	return true
}

func (w *yiddishWord) r1plus3() bool { return w.p1 <= w.c+6 }

// Candidate selection precedes the action's region check. An action failure
// never exposes a shorter suffix in the same among pass.
func (w *yiddishWord) suffix(rules []yiddishRule) (yiddishRule, bool) {
	var best yiddishRule
	for _, r := range rules {
		if len(r.ending) > len(best.ending) && w.at(w.c-len(r.ending), r.ending) { best = r }
	}
	if best.ending == "" { return best, false }
	w.c -= len(best.ending)
	return best, true
}

func (w *yiddishWord) primary() {
	end := w.c
	r, ok := w.suffix(yiddishPrimary[:])
	if !ok { return }
	start := w.c
	switch r.action {
	case yiddishDelete:
		if w.p1 <= start { w.replace(start, end, "") }
	case yiddishRewrite:
		w.replace(start, end, r.replacement)
	case yiddishIES:
		if w.p1 <= start { w.replace(start, end, "יע") }
	case yiddishOS:
		if w.p1 <= start { w.replace(start, end, "ה") }
	case yiddishNested:
		if w.p1 > start { break }
		w.replace(start, end, "")
		if nested, ok := w.suffix(yiddishIrregular[:]); ok { w.replace(w.c, start, nested.replacement) }
	case yiddishTes:
		if w.p1 <= start { w.replace(start, end, "") } else { w.replace(start, end, "ט") }
		// The optional literal גע is not an algorithmic GE marker.
		if w.at(start-8, "בראכ") {
			from := start-8
			if w.at(from-4, "גע") { from -= 4 }
			w.replace(from, start, "ברענג")
		}
	case yiddishIST:
		previous, n := w.before(start)
		if previous == 'ג' || previous == 'ש' {
			w.c = start-n
			if w.r1plus3() { w.replace(start, end, "יס") }
		} else if w.p1 <= start { w.replace(start, end, "") }
	}
	w.c = w.length()
}

func (w *yiddishWord) secondary() {
	end := w.c
	if r, ok := w.suffix(yiddishSecondary[:]); ok && w.p1 <= w.c {
		before, _ := w.before(w.c)
		if r.ending != "ל" || yiddishConsonant(before) { w.replace(w.c, end, "") }
	}
	w.c = w.length()
}

func (w *yiddishWord) tertiary() {
	end := w.c
	if r, ok := w.suffix(yiddishTertiary[:]); ok && r.action != yiddishKeep && w.p1 <= w.c { w.replace(w.c, end, "") }
	w.c = w.length()
}

func (w *yiddishWord) cleanup() {
	for w.c > 0 {
		size := 0
		if w.at(w.c-2, "GE") { size = 2 } else if w.at(w.c-3, "TSU") { size = 3 }
		if size != 0 { w.replace(w.c-size, w.c, ""); w.c -= size; continue }
		_, n := w.before(w.c)
		w.c -= n
	}
}

func yiddishStem(text string) string {
	w := yiddishWord{text: text}
	w.prelude()
	w.markRegions()
	w.c = w.length()
	w.primary()
	w.secondary()
	w.tertiary()
	w.cleanup()
	return w.result()
}
