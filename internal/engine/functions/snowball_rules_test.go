package functions

import "testing"

func snowballEnglishStages(t *testing.T) {
	t.Helper()
	// Isolated real steps keep each selected suffix observable rather than
	// allowing a later step to conceal an incorrect intermediate rule.
	for _, tc := range []struct {
		suffix, replacement string
	}{
		{"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"},
		{"abli", "able"}, {"entli", "ent"}, {"izer", "ize"},
		{"ization", "ize"}, {"ational", "ate"}, {"ation", "ate"},
		{"ator", "ate"}, {"alism", "al"}, {"aliti", "al"},
		{"alli", "al"}, {"fulness", "ful"}, {"ousli", "ous"},
		{"ousness", "ous"}, {"iveness", "ive"}, {"iviti", "ive"},
		{"biliti", "ble"}, {"bli", "ble"}, {"fulli", "ful"},
		{"lessli", "less"}, {"li", ""},
	} {
		w := englishWord{letters: []rune("abac" + tc.suffix), p1: 2, p2: 4}
		w.step2()
		if got := string(w.letters); got != "abac"+tc.replacement {
			t.Errorf("step2 %s: %s", tc.suffix, got)
		}
	}
	for _, tc := range []struct{ suffix, replacement string }{
		{"tional", "tion"}, {"ational", "ate"}, {"alize", "al"},
		{"icate", "ic"}, {"iciti", "ic"}, {"ical", "ic"},
		{"ful", ""}, {"ness", ""}, {"ative", ""},
	} {
		w := englishWord{letters: []rune("abac" + tc.suffix), p1: 2, p2: 4}
		w.step3()
		if got := string(w.letters); got != "abac"+tc.replacement {
			t.Errorf("step3 %s: %s", tc.suffix, got)
		}
	}
	for _, suffix := range []string{"al", "ance", "ence", "er", "ic", "able", "ible", "ant", "ement", "ment", "ent", "ism", "ate", "iti", "ous", "ive", "ize"} {
		w := englishWord{letters: []rune("abac" + suffix), p1: 2, p2: 4}
		w.step4()
		if got := string(w.letters); got != "abac" {
			t.Errorf("step4 %s: %s", suffix, got)
		}
	}
	for _, tc := range []struct {
		input, want string
		p1, p2      int
		step        int
	}{
		{"abaclogi", "abaclog", 2, 4, 2},
		{"abacogi", "abacogi", 2, 4, 2}, // selected ogi must not retry li
		{"abapli", "abapli", 2, 4, 2},
		{"abacational", "abacational", 5, 6, 2}, // longest suffix outside R1
		{"abacative", "abacative", 2, 5, 3},
		{"abaction", "abact", 2, 4, 4},
		{"abacsion", "abacs", 2, 4, 4},
		{"abacion", "abacion", 2, 4, 4},
		{"abacement", "abacement", 2, 5, 4}, // no shorter ment/ent retry
		{"rate", "rate", 3, 4, 5},
		{"abace", "abac", 2, 4, 5},
		{"aball", "abal", 2, 4, 5},
		{"aball", "aball", 2, 5, 5},
	} {
		w := englishWord{letters: []rune(tc.input), p1: tc.p1, p2: tc.p2}
		switch tc.step {
		case 2:
			w.step2()
		case 3:
			w.step3()
		case 4:
			w.step4()
		case 5:
			w.step5()
		}
		if got := string(w.letters); got != tc.want {
			t.Errorf("step%d %s: %s, want %s", tc.step, tc.input, got, tc.want)
		}
	}
	for _, double := range []string{"bb", "dd", "ff", "gg", "mm", "nn", "pp", "rr", "tt"} {
		w := englishWord{letters: []rune("ab" + double + "ing"), p1: 2, p2: 4}
		w.step1b()
		if got := string(w.letters); got != "ab"+double[:1] {
			t.Errorf("step1b double %s: %s", double, got)
		}
	}
}
