package engine

import (
	"errors"
	"testing"
	"unicode/utf8"

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
			got := boundedNameDistance(tc.candidate, key, make([]int, width), make([]int, width), tc.limit)
			if got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}

func TestQueryAliasesHintOccurrences(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	ref := roleTableRef(t, e, "t")
	for _, tc := range []struct {
		name      string
		columns   []string
		qualifier string
		want      string
	}{
		{"one", []string{"iid"}, "x", `Perhaps you meant to reference the column "x.iid".`},
		{"two", []string{"iid", "idx"}, "", `Perhaps you meant to reference the column "x.iid" or the column "x.idx".`},
		{"three", []string{"iid", "idx", "idd"}, "", ""},
		{"same_spelling_two", []string{"iid", "iid"}, "", `Perhaps you meant to reference the column "x.iid" or the column "x.iid".`},
		{"same_spelling_three", []string{"iid", "iid", "iid"}, "", ""},
		{"better_resets_ties", []string{"iid", "idx", "idd", "id"}, "X", `Perhaps you meant to reference the column "x.id".`},
		{"case_sensitive_qualifier", []string{"iid"}, "X", `Perhaps you meant to reference the column "x.iid".`},
		{"distant_qualifier", []string{"id"}, "extraordinarilylongname", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := queryQualifier{name: "x", ref: ref}
			for _, name := range tc.columns {
				entry.columns = append(entry.columns, queryColumn{descriptor: catalog.Column{Name: name}})
			}
			// Synthetic whole-join entries must not add a third tie or win.
			join := queryQualifier{name: "j", columns: []queryColumn{{descriptor: catalog.Column{Name: "id"}}}, excludeColumnDiagnostics: true}
			if detail, hint, err := queryColumnDiagnostic([]queryQualifier{entry, join}, tc.qualifier, "id", parser.Position{Offset: -1}, false); err != nil || detail != "" || hint != tc.want {
				t.Fatal(detail, hint, err, tc.want)
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
		err := valuesOrderColumnError([]catalog.Column{{Name: "column1"}}, tc.name, parser.Position{Offset: -1})
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "` + tc.name + `" does not exist`, Hint: tc.hint}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
}

func TestQueryAliasesUnicodeHintCutoff(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	ref := roleTableRef(t, e, "t")
	entry := queryQualifier{name: "x", ref: ref, columns: []queryColumn{{descriptor: catalog.Column{Name: "aa"}}}}
	// Two character substitutions are allowed because PostgreSQL's cutoff
	// uses the four UTF8 bytes of the request, not its two characters.
	if detail, hint, err := queryColumnDiagnostic([]queryQualifier{entry}, "x", "éé", parser.Position{Offset: -1}, false); err != nil || detail != "" || hint != `Perhaps you meant to reference the column "x.aa".` {
		t.Fatal(detail, hint, err)
	}
}

func TestQueryAliasesExactDiagnosticPrecedence(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	base := queryQualifier{name: "t", ref: roleTableRef(t, e, "t"), columns: []queryColumn{{descriptor: catalog.Column{Name: "id"}}}}
	// Separate self-reference entries count even when physical refs coincide.
	other := base
	other.name = "u"
	fuzzy := base
	fuzzy.name = "x"
	fuzzy.columns = []queryColumn{{descriptor: catalog.Column{Name: "iid"}}}
	join := queryQualifier{name: "j", columns: base.columns, excludeColumnDiagnostics: true}
	for _, tc := range []struct {
		name, qualifier string
		entries         []queryQualifier
		detail, hint    string
	}{
		{"single_over_fuzzy", "", []queryQualifier{fuzzy, base, join}, `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`, ""},
		{"multiple", "", []queryQualifier{base, other}, `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, "Try using a table-qualified name."},
		{"three", "", []queryQualifier{base, other, base}, `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, "Try using a table-qualified name."},
		{"qualified_exact", "t", []queryQualifier{base, other}, `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`, ""},
		{"case_sensitive_qualifier", "T", []queryQualifier{base}, "", `Perhaps you meant to reference the column "t.id".`},
		{"synthetic_join_excluded", "", []queryQualifier{join}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail, hint, err := queryColumnDiagnostic(tc.entries, tc.qualifier, "id", parser.Position{Offset: -1}, false)
			if err != nil || detail != tc.detail || hint != tc.hint {
				t.Fatal(detail, hint, err, tc.detail, tc.hint)
			}
		})
	}
}

func TestQueryAliasesHiddenDuplicateDiagnostic(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	base := queryQualifier{name: "t", ref: roleTableRef(t, e, "t"), columns: []queryColumn{{descriptor: catalog.Column{Name: "id"}}}}
	other := base
	other.name = "u"
	duplicate := base
	duplicate.name = "x"
	duplicate.columns = []queryColumn{{descriptor: catalog.Column{Name: "id"}}, {descriptor: catalog.Column{Name: "id"}}}
	for _, tc := range []struct {
		name, qualifier string
		entries         []queryQualifier
	}{
		{"unqualified", "", []queryQualifier{duplicate}},
		{"qualified_exact", "x", []queryQualifier{duplicate}},
		{"qualifier_penalty", "j", []queryQualifier{duplicate}},
		{"distant_qualifier", "extraordinarilylongname", []queryQualifier{duplicate}},
		{"after_plural", "", []queryQualifier{base, other, duplicate}},
		{"after_plural_qualifier_mismatch", "t", []queryQualifier{base, base, duplicate}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Character, not byte Offset, supplies the positioned diagnostic.
			detail, hint, err := queryColumnDiagnostic(tc.entries, tc.qualifier, "id", parser.Position{Offset: 15, Character: 14}, false)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: `column reference "id" is ambiguous`, Position: "14"}
			if detail != "" || hint != "" || !errors.As(err, &got) || *got != want {
				t.Fatal(detail, hint, got, err, want)
			}
		})
	}
	// Join RTEs are excluded even if their exposed aliases repeat the name.
	join := duplicate
	join.excludeColumnDiagnostics = true
	detail, hint, err := queryColumnDiagnostic([]queryQualifier{join}, "", "id", parser.Position{Character: 8}, false)
	if err != nil || detail != "" || hint != "" {
		t.Fatal(detail, hint, err)
	}
}

func TestQueryValuesMissingColumnDiagnostics(t *testing.T) {
	entry := queryQualifier{name: "v", columns: []queryColumn{{descriptor: catalog.Column{Name: "n"}}}}
	detail, hint, err := queryColumnDiagnostic([]queryQualifier{entry}, "v", "nn", parser.Position{Character: 8}, false)
	if err != nil || detail != "" || hint != `Perhaps you meant to reference the column "v.n".` {
		t.Fatal(detail, hint, err)
	}
	for _, nonLateral := range []bool{false, true} {
		detail, hint, err = queryColumnDiagnostic([]queryQualifier{entry}, "", "n", parser.Position{Character: 8}, nonLateral)
		wantHint := ""
		if nonLateral {
			wantHint = "To reference that column, you must mark this subquery with LATERAL."
		}
		if err != nil || detail != `There is a column named "n" in table "v", but it cannot be referenced from this part of the query.` || hint != wantHint {
			t.Fatal(detail, hint, err)
		}
	}
	entry.excludeColumnDiagnostics = true
	for _, name := range []string{"n", "nn"} {
		detail, hint, err = queryColumnDiagnostic([]queryQualifier{entry}, "", name, parser.Position{Character: 8}, true)
		if err != nil || detail != "" || hint != "" {
			t.Fatal("excluded join diagnostic", detail, hint, err)
		}
	}
}
