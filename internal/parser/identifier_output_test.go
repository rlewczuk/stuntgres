package parser

import "testing"

func TestIdentifierOutputQuoting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		quoted bool
	}{
		{"s", false}, {"a_1", false}, {"_s", false}, {"abort", false},
		{"", true}, {"1s", true}, {"A", true}, {"a b", true}, {"a\"b", true},
		{"select", true}, {"between", true}, {"authorization", true}, {"é", true},
	} {
		if got := IdentifierNeedsQuotes(tc.name); got != tc.quoted {
			t.Errorf("%q: got %v want %v", tc.name, got, tc.quoted)
		}
	}
}
