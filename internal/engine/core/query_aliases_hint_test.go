package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestQueryAliasesHintOccurrences(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	ref := enginetest.RoleTableRef(t, e, "t")
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
			entry := analyze.QueryQualifier{Name: "x", Ref: ref}
			for _, name := range tc.columns {
				entry.Columns = append(entry.Columns, analyze.QueryColumn{Descriptor: catalog.Column{Name: name}})
			}
			// Synthetic whole-join entries must not add a third tie or win.
			join := analyze.QueryQualifier{Name: "j", Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "id"}}}, ExcludeColumnDiagnostics: true}
			if detail, hint, err := analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{entry, join}, tc.qualifier, "id", parser.Position{Offset: -1}, false); err != nil || detail != "" || hint != tc.want {
				t.Fatal(detail, hint, err, tc.want)
			}
		})
	}
}

func TestQueryAliasesUnicodeHintCutoff(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	ref := enginetest.RoleTableRef(t, e, "t")
	entry := analyze.QueryQualifier{Name: "x", Ref: ref, Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "aa"}}}}
	// Two character substitutions are allowed because PostgreSQL's cutoff
	// uses the four UTF8 bytes of the request, not its two characters.
	if detail, hint, err := analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{entry}, "x", "éé", parser.Position{Offset: -1}, false); err != nil || detail != "" || hint != `Perhaps you meant to reference the column "x.aa".` {
		t.Fatal(detail, hint, err)
	}
}

func TestQueryAliasesExactDiagnosticPrecedence(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	base := analyze.QueryQualifier{Name: "t", Ref: enginetest.RoleTableRef(t, e, "t"), Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "id"}}}}
	// Separate self-reference entries count even when physical refs coincide.
	other := base
	other.Name = "u"
	fuzzy := base
	fuzzy.Name = "x"
	fuzzy.Columns = []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "iid"}}}
	join := analyze.QueryQualifier{Name: "j", Columns: base.Columns, ExcludeColumnDiagnostics: true}
	for _, tc := range []struct {
		name, qualifier string
		entries         []analyze.QueryQualifier
		detail, hint    string
	}{
		{"single_over_fuzzy", "", []analyze.QueryQualifier{fuzzy, base, join}, `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`, ""},
		{"multiple", "", []analyze.QueryQualifier{base, other}, `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, "Try using a table-qualified name."},
		{"three", "", []analyze.QueryQualifier{base, other, base}, `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, "Try using a table-qualified name."},
		{"qualified_exact", "t", []analyze.QueryQualifier{base, other}, `There is a column named "id" in table "t", but it cannot be referenced from this part of the query.`, ""},
		{"case_sensitive_qualifier", "T", []analyze.QueryQualifier{base}, "", `Perhaps you meant to reference the column "t.id".`},
		{"synthetic_join_excluded", "", []analyze.QueryQualifier{join}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail, hint, err := analyze.QueryColumnDiagnostic(tc.entries, tc.qualifier, "id", parser.Position{Offset: -1}, false)
			if err != nil || detail != tc.detail || hint != tc.hint {
				t.Fatal(detail, hint, err, tc.detail, tc.hint)
			}
		})
	}
}

func TestQueryAliasesHiddenDuplicateDiagnostic(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	base := analyze.QueryQualifier{Name: "t", Ref: enginetest.RoleTableRef(t, e, "t"), Columns: []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "id"}}}}
	other := base
	other.Name = "u"
	duplicate := base
	duplicate.Name = "x"
	duplicate.Columns = []analyze.QueryColumn{{Descriptor: catalog.Column{Name: "id"}}, {Descriptor: catalog.Column{Name: "id"}}}
	for _, tc := range []struct {
		name, qualifier string
		entries         []analyze.QueryQualifier
	}{
		{"unqualified", "", []analyze.QueryQualifier{duplicate}},
		{"qualified_exact", "x", []analyze.QueryQualifier{duplicate}},
		{"qualifier_penalty", "j", []analyze.QueryQualifier{duplicate}},
		{"distant_qualifier", "extraordinarilylongname", []analyze.QueryQualifier{duplicate}},
		{"after_plural", "", []analyze.QueryQualifier{base, other, duplicate}},
		{"after_plural_qualifier_mismatch", "t", []analyze.QueryQualifier{base, base, duplicate}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Character, not byte Offset, supplies the positioned diagnostic.
			detail, hint, err := analyze.QueryColumnDiagnostic(tc.entries, tc.qualifier, "id", parser.Position{Offset: 15, Character: 14}, false)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: `column reference "id" is ambiguous`, Position: "14"}
			if detail != "" || hint != "" || !errors.As(err, &got) || *got != want {
				t.Fatal(detail, hint, got, err, want)
			}
		})
	}
	// Join RTEs are excluded even if their exposed aliases repeat the name.
	join := duplicate
	join.ExcludeColumnDiagnostics = true
	detail, hint, err := analyze.QueryColumnDiagnostic([]analyze.QueryQualifier{join}, "", "id", parser.Position{Character: 8}, false)
	if err != nil || detail != "" || hint != "" {
		t.Fatal(detail, hint, err)
	}
}
