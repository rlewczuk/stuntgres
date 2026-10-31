package functions

import "testing"

// Independent stage witnesses transcribed directly from the released SBL
// Step_3 source, not from the production Go tables or native execution.
// A non-alphabetic prefix isolates each among entry; R1 is its byte boundary.
func testSerbianStep3Witnesses(t *testing.T) {
	t.Helper()
	for _, tc := range []struct { suffix, replacement string; guarded bool }{
		{"enom", "", false},
		{"enoj", "", false},
		{"enog", "", false},
		{"enim", "", false},
		{"enih", "", false},
		{"anoj", "", false},
		{"anog", "", false},
		{"anim", "", false},
		{"anih", "", false},
		{"ost", "", false},
		{"eno", "", false},
		{"eni", "", false},
		{"oga", "", false},
		{"ima", "", false},
		{"enu", "", false},
		{"ena", "", false},
		{"ama", "", false},
		{"ano", "", false},
		{"ani", "", false},
		{"om", "", false},
		{"og", "", false},
		{"u", "", false},
		{"o", "", false},
		{"i", "", false},
		{"e", "", false},
		{"a", "", false},
	} {
		for _, noDiacritics := range []bool{true, false} {
			input := "|" + tc.suffix
			w := serbianWord{prefix: input}
			ok := w.step(serbianStep3[:], serbianStep3Max, 1, noDiacritics)
			want, accepted := "|"+tc.replacement, true
			if tc.guarded && !noDiacritics { want, accepted = input, false }
			if got := w.prefix+w.tail; got != want || ok != accepted {
				t.Errorf("Step_3 %q no_diacritics=%t: %q/%t want %q/%t", tc.suffix, noDiacritics, got, ok, want, accepted)
			}
		}
	}
}
