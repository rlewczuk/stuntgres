package fdw

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestFDWImportSelected(t *testing.T) {
	filter := []parser.Identifier{{Text: "a"}, {Text: "b"}}
	cases := []struct {
		name   string
		s      parser.ForeignDDL
		table  string
		wanted bool
	}{
		{"no filter", parser.ForeignDDL{}, "x", true},
		{"no filter with except", parser.ForeignDDL{ImportExcept: true}, "x", true},
		{"limit to listed", parser.ForeignDDL{ImportFilter: filter}, "b", true},
		{"limit to unlisted", parser.ForeignDDL{ImportFilter: filter}, "x", false},
		{"limit to is case sensitive", parser.ForeignDDL{ImportFilter: filter}, "A", false},
		{"except listed", parser.ForeignDDL{ImportFilter: filter, ImportExcept: true}, "a", false},
		{"except unlisted", parser.ForeignDDL{ImportFilter: filter, ImportExcept: true}, "x", true},
	}
	for _, c := range cases {
		if got := ImportSelected(&c.s, c.table); got != c.wanted {
			t.Errorf("%s: ImportSelected(%q) = %v, want %v", c.name, c.table, got, c.wanted)
		}
	}
}
