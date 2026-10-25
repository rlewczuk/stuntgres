package engine

import (
	"errors"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestQueryAliasesNameDistance(t *testing.T) {
	for _, tc := range []struct {
		candidate, request string
		limit, want        int
	}{
		{"iid", "id", 1, 1}, {"iíd", "id", 1, 1}, {"id", "iíd", 1, 1},
		{"éclair", "éclair", 0, 0}, {"éclair", "eclair", 1, 1},
		{"Key", "key", 0, 1}, {"Key", "key", 1, 1},
		{"", "é", 1, 1}, {"é", "", 1, 1}, {"abcde", "id", 1, 2},
		{"abc", "xyz", 2, 3}, {"abc", "xyz", 3, 3},
	} {
		t.Run(tc.candidate+"/"+tc.request, func(t *testing.T) {
			key := make([]int, 0, utf8.RuneCountInString(tc.request))
			for _, r := range tc.request {
				key = append(key, int(r))
			}
			width := len(key) + 1
			got := analyze.BoundedNameDistance(tc.candidate, key, make([]int, width), make([]int, width), tc.limit)
			if got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}

func TestQueryAliasesSharedValuesDistance(t *testing.T) {
	for _, tc := range []struct {
		name string
		hint string
	}{
		{"colum1", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"colümn1", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"no_such_column", ""},
	} {
		err := analyze.ValuesOrderColumnError([]catalog.Column{{Name: "column1"}}, tc.name, parser.Position{Offset: -1})
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "` + tc.name + `" does not exist`, Hint: tc.hint}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
}

func TestQueryValuesMissingColumnDiagnostics(t *testing.T) {
	entry := analyze.QueryQualifier{Name: "v", Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "n"}}}}
	detail, hint, err := analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{entry}, "v", "nn", parser.Position{Character: 8}, false)
	if err != nil || detail != "" || hint != `Perhaps you meant to reference the column "v.n".` {
		t.Fatal(detail, hint, err)
	}
	for _, nonLateral := range []bool{false, true} {
		detail, hint, err = analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{entry}, "", "n", parser.Position{Character: 8}, nonLateral)
		wantHint := ""
		if nonLateral {
			wantHint = "To reference that column, you must mark this subquery with LATERAL."
		}
		if err != nil || detail != `There is a column named "n" in table "v", but it cannot be referenced from this part of the query.` || hint != wantHint {
			t.Fatal(detail, hint, err)
		}
	}
	entry.ExcludeColumnDiagnostics = true
	for _, name := range []string{"n", "nn"} {
		detail, hint, err = analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{entry}, "", name, parser.Position{Character: 8}, true)
		if err != nil || detail != "" || hint != "" {
			t.Fatal("excluded join diagnostic", detail, hint, err)
		}
	}
}
