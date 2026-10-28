package fdw

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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

func TestFDWImportError(t *testing.T) {
	source := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "table_name=import_b"`, Context: `SQL function "fdw_import_validator" statement 2`}
	want := *source
	want.Context += "\nimporting foreign table \"import_b\""
	for _, err := range []error{source, fmt.Errorf("routine execution: %w", source)} {
		got := ImportError(err, "import_b")
		var diagnostic *sqlstate.Error
		if !errors.As(got, &diagnostic) || !reflect.DeepEqual(diagnostic, &want) || !errors.Is(got, err) {
			t.Fatalf("wrapped import diagnostic: got %#v want %#v", diagnostic, &want)
		}
	}
	if source.Context != `SQL function "fdw_import_validator" statement 2` {
		t.Fatal("annotation mutated shared routine diagnostic")
	}
	if err := ImportError(context.Canceled, "import_b"); err != context.Canceled {
		t.Fatal("annotation replaced ordinary cancellation", err)
	}
}
