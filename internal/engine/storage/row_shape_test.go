package storage

import "testing"

func TestRowShapeMatchesIgnoresEndsOnly(t *testing.T) {
	ends := &DeletionRoot{}
	original := &Relation{Versions: make([]RowVersion, 2), Ends: ends, Generation: 3}
	shape := RowShape{Original: original, ObservedLength: 2, ObservedEnds: ends, Generation: 3}
	if !shape.Matches(original) || !shape.MatchesWithEnds(original) {
		t.Fatal("unchanged predecessor did not match")
	}
	original.Ends = &DeletionRoot{}
	if !shape.Matches(original) {
		t.Fatal("Matches rejected a deletion-ends change")
	}
	if shape.MatchesWithEnds(original) {
		t.Fatal("MatchesWithEnds accepted a deletion-ends change")
	}
	original.Ends = ends
	cases := map[string]func() (*Relation, func()){
		"other store": func() (*Relation, func()) {
			copy := *original
			return &copy, func() {}
		},
		"generation": func() (*Relation, func()) {
			original.Generation++
			return original, func() { original.Generation-- }
		},
		"length": func() (*Relation, func()) {
			original.Versions = append(original.Versions, RowVersion{})
			return original, func() { original.Versions = original.Versions[:2] }
		},
	}
	for name, change := range cases {
		current, restore := change()
		if shape.Matches(current) || shape.MatchesWithEnds(current) {
			t.Fatalf("%s change matched", name)
		}
		restore()
	}
}
