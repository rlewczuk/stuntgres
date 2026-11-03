package pgcatalog

import "testing"

func TestStatioAllIndexesMembership(t *testing.T) {
	// ClassKind has no native TOAST descriptor facility today. Test the exact
	// production-called release predicate without manufacturing TOAST storage.
	for _, tc := range []struct {
		kind     string
		included bool
	}{
		{"r", true}, {"t", true}, {"m", true},
		{"p", false}, {"v", false}, {"f", false}, {"S", false},
		{"c", false}, {"i", false}, {"I", false}, {"", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if got := statioIndexParentKind(tc.kind); got != tc.included {
				t.Fatalf("parent kind %q included=%v want %v", tc.kind, got, tc.included)
			}
		})
	}
}
