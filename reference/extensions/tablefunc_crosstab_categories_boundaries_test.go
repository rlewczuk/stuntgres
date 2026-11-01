package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source, categories, declaration string
		columns, types                       []string
		rows                                 [][]any
		bag                                  bool
	}{
		{"one_category", "SELECT 'g'::text,'a'::text,'v'::text", "SELECT 'a'::text", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "v"}}, false},
		{"adjacent_null_keys_and_first_null_extra", "SELECT k,extra,category,v FROM (VALUES(1,NULL::text,NULL::text,'a'::text,'first'::text),(2,NULL,'later','b','second')) AS s(seq,k,extra,category,v) ORDER BY seq", crosstabCategoriesOrder("a", "b"), "k text,extra text,a text,b text", []string{"k", "extra", "a", "b"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, "first", "second"}}, false},
		{"matched_and_final_null", "SELECT k,category,v FROM (VALUES(1,'g'::text,'a'::text,NULL::text),(2,'g','b','old'),(3,'g','b',NULL)) AS s(seq,k,category,v) ORDER BY seq", crosstabCategoriesOrder("a", "b"), "k text,a text,b text", []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"g", nil, nil}}, false},
		{"null_and_unmatched_categories", "SELECT k,category,v FROM (VALUES(1,'g'::text,NULL::text,'bad'::text),(2,'g','ignored','bad')) AS s(seq,k,category,v) ORDER BY seq", crosstabCategoriesOrder("a", "b"), "k text,a text,b text", []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"g", nil, nil}}, false},
		{"separated_groups", "SELECT k,category,v FROM (VALUES(1,'a'::text,'x'::text,'a1'::text),(2,'b','x','b1'),(3,'a','x','a2')) AS s(seq,k,category,v) ORDER BY seq", "SELECT 'x'::text", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a1"}, {"b", "b1"}, {"a", "a2"}}, true},
		{"source_reverse_category_order", "SELECT k,category,v FROM (VALUES(1,'g'::text,'a'::text,'A'::text),(2,'g','b','B')) AS s(seq,k,category,v) ORDER BY seq DESC", crosstabCategoriesOrder("b", "a"), "k text,b text,a text", []string{"k", "b", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"g", "B", "A"}}, false},
		{"both_empty", "SELECT 1,2,3 WHERE false", "SELECT 1 WHERE false", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil, false},
		{"empty_source_one_column", "SELECT 1 WHERE false", "SELECT 'a'::text", "k text,a integer,b integer", []string{"k", "a", "b"}, []string{"TEXT", "INT4", "INT4"}, nil, false},
		{"empty_source_two_columns", "SELECT 1,2 WHERE false", "SELECT 'a'::text", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil, false},
		{"empty_source_width_mismatch", "SELECT 1,2,3,4 WHERE false", "SELECT 'a'::text", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil, false},
		{"empty_multicolumn_categories", "SELECT 1,2,3 WHERE false", "SELECT 1,2 WHERE false", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil, false},
		{"empty_query_strings", "", "", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil, false},
		{"last_select_per_string", "SELECT 99; SELECT 'g'::text,'a'::text,'v'::text", "SELECT 'unused'::text; SELECT 'a'::text", "k text,a text", []string{"k", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "v"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION tablefunc", 0))
				s := fixture.Select(crosstabCategoriesSQL("crosstab", tc.declaration), tc.columns, tc.types, tc.rows, tc.source, tc.categories)
				s.Bag = tc.bag
				crosstabCategoriesBoth(t, p, b, c, s)
			})
		})
	}
	for _, tc := range []struct {
		name, first, second string
		duplicate           bool
	}{
		{"distinct_at_byte61", strings.Repeat("x", 60) + "a", strings.Repeat("x", 60) + "b", false},
		{"distinct_at_byte62", strings.Repeat("x", 61) + "a", strings.Repeat("x", 61) + "b", false},
		{"same_first62", strings.Repeat("x", 62) + "a", strings.Repeat("x", 62) + "b", true},
		{"partial_utf8_prefix", strings.Repeat("x", 61) + "é", strings.Repeat("x", 61) + "ê", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION tablefunc", 0))
				q := crosstabCategoriesSQL("crosstab", "k text,a text,b text")
				source := "SELECT k,category,v FROM (VALUES(1,'g'::text," + fixture.CrosstabQuote(tc.first) + "::text,'A'::text),(2,'g'," + fixture.CrosstabQuote(tc.second) + ",'B')) AS s(seq,k,category,v) ORDER BY seq"
				categories := crosstabCategoriesOrder(tc.first, tc.second)
				if tc.duplicate {
					crosstabFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42710", "duplicate category name"), "execute", source, categories)
				} else {
					crosstabCategoriesBoth(t, p, b, c, crosstabCategoriesRows(q, [][]any{{"g", "A", "B"}}, source, categories))
				}
				// Insertion and lookup share byte truncation, including an incomplete UTF8 rune.
				q = crosstabCategoriesSQL("crosstab", "k text,a text")
				lookup := tc.first
				if len(tc.first) >= 62 {
					lookup += "-long-source-suffix"
				}
				if tc.name == "partial_utf8_prefix" {
					lookup = tc.second
				}
				crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "value"}}, "SELECT 'g'::text,"+fixture.CrosstabQuote(lookup)+"::text,'value'::text", "SELECT "+fixture.CrosstabQuote(tc.first)+"::text"))
			})
		})
	}
}
