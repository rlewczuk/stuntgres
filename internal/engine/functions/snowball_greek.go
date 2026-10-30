package functions

import "unicode/utf8"

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_greek.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/greek.sbl.
// BSD license: see snowball_LICENSE.txt. The dictionary wrapper owns ASCII
// lowercasing, stopwords, the original-byte limit and empty-result restoration.
// Snowball's `do` restores its cursor, NOT text: every stage below keeps edits
// made before an unsuccessful subsequent condition.
type greekState struct {
	w     []rune
	test1 bool
}

func greekLower(c rune) rune {
	switch c {
	case 'Ά', 'ά':
		return 'α'
	case 'Έ', 'έ':
		return 'ε'
	case 'Ή', 'ή', 'ΐ', 'ϊ':
		return 'η'
	case 'Ί', 'ί', 'Ϊ':
		return 'ι'
	case 'Ό', 'ό':
		return 'ο'
	case 'Ύ', 'ύ', 'Ϋ', 'ϋ', 'ΰ':
		return 'υ'
	case 'Ώ', 'ώ':
		return 'ω'
	case 'ς':
		return 'σ'
	}
	if c >= 'Α' && c <= 'Ω' && c != '\u03a2' {
		return c + ('α' - 'Α')
	}
	return c
}

// greekEnd returns a rune count without converting or allocating the suffix.
func greekEnd(w []rune, suffix string) int {
	n := len(w)
	for len(suffix) != 0 {
		c, size := utf8.DecodeLastRuneInString(suffix)
		if n == 0 || w[n-1] != c {
			return 0
		}
		n--
		suffix = suffix[:len(suffix)-size]
	}
	return len(w) - n
}

// Literal, space-separated rule sets keep all matching allocation-free. Choose
// the longest member, including when one exception is a suffix of another.
func greekMatch(w []rune, set string, exact bool) int {
	best := 0
	for start := 0; start < len(set); {
		end := start
		for end < len(set) && set[end] != ' ' {
			end++
		}
		n := greekEnd(w, set[start:end])
		if n > best && (!exact || n == len(w)) {
			best = n
		}
		start = end + 1
	}
	return best
}

func (g *greekState) has(set string, exact bool) bool { return greekMatch(g.w, set, exact) != 0 }
func (g *greekState) add(s string) {
	for _, c := range s {
		g.w = append(g.w, c)
	}
}
func (g *greekState) cut(set string, clear bool) bool {
	n := greekMatch(g.w, set, false)
	if n == 0 {
		return false
	}
	g.w = g.w[:len(g.w)-n]
	if clear {
		g.test1 = false
	}
	return true
}
func (g *greekState) vowel(v2 bool) bool {
	if len(g.w) == 0 {
		return false
	}
	switch g.w[len(g.w)-1] {
	case 'α', 'ε', 'η', 'ι', 'ο', 'ω':
		return true
	case 'υ':
		return !v2
	}
	return false
}

func greekStem(word string) string {
	// No Greek rule can affect an ASCII-only token; preserve its backing too.
	greek := false
	for _, c := range word {
		if c >= '\u0370' && c <= '\u03ff' {
			greek = true
			break
		}
	}
	if !greek {
		return word
	}
	var scratch [1004]rune
	n := utf8.RuneCountInString(word)
	w := scratch[:0]
	if n+4 > len(scratch) {
		w = make([]rune, 0, n+4)
	}
	for _, c := range word {
		w = append(w, greekLower(c))
	}
	g := greekState{w: w, test1: true}
	if len(w) >= 3 {
		g.step1()
		g.s1()
		g.s2()
		g.s3()
		g.s4()
		g.s5()
		g.s6()
		g.s7()
		g.s8()
		g.s9()
		g.s10()
		g.step2a()
		g.step2b()
		g.step2c()
		g.step2d()
		g.step3()
		g.step4()
		g.step5a()
		g.step5b()
		g.step5c()
		g.step5d()
		g.step5e()
		g.step5f()
		g.step5g()
		g.step5h()
		g.step5j()
		g.step5i()
		g.step5k()
		g.step5l()
		g.step5m()
		g.step6()
		g.step7()
	}
	return string(g.w)
}

// Released replacement classes, immutable after package initialization.
var greekIrregular = [...]struct{ set, replacement string }{
	{"φαγια φαγιου φαγιων", "φα"}, {"σκαγια σκαγιου σκαγιων", "σκα"},
	{"ολογιου ολογια ολογιων", "ολο"}, {"σογιου σογια σογιων", "σο"},
	{"τατογια τατογιου τατογιων", "τατο"}, {"κρεασ κρεατοσ κρεατα κρεατων", "κρε"},
	{"περασ περατοσ περατη περατα περατων", "περ"}, {"τερασ τερατοσ τερατα τερατων", "τερ"},
	{"φωσ φωτοσ φωτα φωτων", "φω"}, {"καθεστωσ καθεστωτοσ καθεστωτα καθεστωτων", "καθεστ"},
	{"γεγονοσ γεγονοτοσ γεγονοτα γεγονοτων", "γεγον"},
}

func (g *greekState) step1() {
	best, replacement := 0, ""
	for _, group := range greekIrregular {
		if n := greekMatch(g.w, group.set, false); n > best {
			best, replacement = n, group.replacement
		}
	}
	if best != 0 {
		g.w = g.w[:len(g.w)-best]
		g.add(replacement)
		g.test1 = false
	}
}

func (g *greekState) s1() {
	if !g.cut("ιζα ιζεσ ιζε ιζαμε ιζατε ιζαν ιζανε ιζω ιζεισ ιζει ιζουμε ιζετε ιζουν ιζουνε", true) {
		return
	}
	if g.has("αναμπα εμπα επα ξαναπα πα περιπα αθρο συναθρο δανε", true) {
		g.add("ι")
	} else if g.has("μαρκ κορν αμπαρ αρρ βαθυρι βαρκ β βολβορ γκρ γλυκορ γλυκυρ ιμπ λ λου μαρ μ πρ μπρ πολυρ π ρ πιπερορ", true) {
		g.add("ιζ")
	}
}
func (g *greekState) s2() {
	if g.cut("ωθηκα ωθηκεσ ωθηκε ωθηκαμε ωθηκατε ωθηκαν ωθηκανε", true) && g.has("αλ βι εν υψ λι ζω σ χ", true) {
		g.add("ων")
	}
}
func (g *greekState) s3() {
	if g.has("ισα", true) {
		g.w = g.w[:2]
		return
	}
	if !g.cut("ισα ισεσ ισε ισαμε ισατε ισαν ισανε", true) {
		return
	}
	if g.has("αναμπα αθρο εμπα εσε εσωκλε επα ξαναπα επε περιπα συναθρο δανε κλε χαρτοπα εξαρχα μετεπε αποκλε απεκλε εκλε πε", true) {
		g.add("ι")
	} else if g.has("αν αφ γε γιγαντοαφ γκε δημοκρατ κομ γκ μ π πουκαμ ολο λαρ", true) {
		g.add("ισ")
	}
}
func (g *greekState) s4() {
	if g.cut("ισω ισεισ ισει ισουμε ισετε ισουν ισουνε", true) && g.has("αναμπα εμπα εσε εσωκλε επα ξαναπα επε περιπα αθρο συναθρο δανε κλε χαρτοπα εξαρχα μετεπε αποκλε απεκλε εκλε πε", true) {
		g.add("ι")
	}
}
func (g *greekState) s5() {
	if !g.cut("ιστοσ ιστου ιστο ιστε ιστοι ιστων ιστουσ ιστη ιστησ ιστα ιστεσ", true) {
		return
	}
	if g.has("δανε συναθρο κλε σε εσωκλε ασε πλε", true) {
		g.add("ι")
	} else if g.has("μ π απ αρ ηδ κτ σκ σχ υψ φα χρ χτ ακτ αορ ασχ ατα αχν αχτ γεμ γυρ εμπ ευπ εχθ ηφα καθ κακ κυλ λυγ μακ μεγ ταχ φιλ χωρ", true) {
		g.add("ιστ")
	}
}
func (g *greekState) s6() {
	if !g.cut("ισμο ισμοι ισμοσ ισμου ισμουσ ισμων", true) {
		return
	}
	if g.has("σε μετασε μικροσε εγκλε αποκλε", true) {
		g.add("ισμ")
		return
	}
	if g.has("δανε αντιδανε", true) {
		g.add("ι")
		return
	}
	best, replacement := 0, ""
	for _, group := range greekS6Rewrites {
		if n := greekEnd(g.w, group.from); n > best {
			best, replacement = n, group.to
		}
	}
	if best != 0 {
		g.w = g.w[:len(g.w)-best]
		g.add(replacement)
	}
}
func (g *greekState) s7() {
	if g.cut("αρακι αρακια ουδακι ουδακια", true) && g.has("σ χ", true) {
		g.add("αρακ")
	}
}
func (g *greekState) s8() {
	if !g.cut("ακι ακια ιτσα ιτσασ ιτσεσ ιτσων αρακι αρακια", true) {
		return
	}
	if g.has("βαμβ βρ καιμ κον κορ λαβρ λουλ μερ μουστ ναγκασ πλ ρ ρυ σ σκ σοκ σπαν τζ φαρμ χ καπακ αλισφ αμβρ ανθρ κ φυλ κατραπ κλιμ μαλ σλοβ φ σφ τσεχοσλοβ", true) {
		g.add("ακ")
	} else if g.has("β βαλ γιαν γλ ζ ηγουμεν καρδ μακρυν νυφ πατερ π τοσ τριπολ", true) || g.has("κορ", false) {
		g.add("ιτσ")
	}
}
func (g *greekState) s9() {
	if g.cut("ιδιο ιδια ιδιων", true) && (g.has("αιφν ιρ ολο ψαλ", true) || g.has("ε παιχν", false)) {
		g.add("ιδ")
	}
}
func (g *greekState) s10() {
	if g.cut("ισκοσ ισκου ισκο ισκε", true) && g.has("δ ιβ μην ρ φραγκ λυκ οβελ", true) {
		g.add("ισκ")
	}
}

var greekS6Rewrites = [...]struct{ from, to string }{
	{"αγνωστικ", "αγνωστ"}, {"ατομικ", "ατομ"}, {"γνωστικ", "γνωστ"}, {"εθνικ", "εθν"},
	{"εκλεκτικ", "εκλεκτ"}, {"σκεπτικ", "σκεπτ"}, {"τοπικ", "τοπ"}, {"αλεξανδριν", "αλεξανδρ"},
	{"βυζαντιν", "βυζαντ"}, {"θεατριν", "θεατρ"},
}
