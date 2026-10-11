package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarMatching(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := []struct {
		expression string
		value      any
	}{
		{`'abc' SIMILAR TO 'abc'`, true}, {`'abc' SIMILAR TO 'b'`, false},
		{`'abc' NOT SIMILAR TO 'b'`, true}, {`'abc' SIMILAR TO '%(b|d)%'`, true},
		{`'abc' SIMILAR TO '(b|c)%'`, false}, {`'' SIMILAR TO '%'`, true},
		{`'é' SIMILAR TO '_'`, true}, {`'é' SIMILAR TO '_'`, false},
		{`'é' SIMILAR TO '__'`, false}, {`chr(10) SIMILAR TO '_'`, true},
		{`('a' || chr(10)) SIMILAR TO '%'`, true}, {`('a' || chr(10)) SIMILAR TO 'a'`, false},
		{`'a.c' SIMILAR TO 'a.c'`, true}, {`'abc' SIMILAR TO 'a.c'`, false},
		{`'^a$' SIMILAR TO '^a$'`, true}, {`'abab' SIMILAR TO '(ab){2}'`, true},
		{`'aaa' SIMILAR TO 'a{2,3}'`, true}, {`'b' SIMILAR TO 'a*b'`, true},
		{`'ab' SIMILAR TO 'a+b'`, true}, {`'b' SIMILAR TO 'a?b'`, true},
		{`'%_' SIMILAR TO '[%][_]'`, true}, {`'a%b' SIMILAR TO 'a!%b' ESCAPE '!'`, true},
		{`'a%b' SIMILAR TO 'aé%b' ESCAPE 'é'`, true}, {`'a\b' SIMILAR TO 'a\b' ESCAPE ''`, true},
		{`'5' SIMILAR TO '\d'`, true}, {`'-abc-' SIMILAR TO '%\mabc\M%'`, true},
		{`'xabcy' SIMILAR TO '%\mabc\M%'`, false}, {`chr(8) SIMILAR TO '\b'`, true},
		{`'\' SIMILAR TO '\B'`, true}, {`NULL::text SIMILAR TO '%'`, nil},
		{`'abc' SIMILAR TO '%' ESCAPE NULL`, nil}, {`NULL::text NOT SIMILAR TO '%'`, nil},
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range cases {
				similarReferenceRoutes(t, p, b, fixture.Select("SELECT "+c.expression+" AS matched", []string{"matched"}, []string{"BOOL"}, [][]any{{c.value}}))
			}
		})
	}
}

func TestReferenceSimilarExtraction(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := []struct {
		source, pattern string
		value           any
	}{
		{"foobar", `%#"o_b#"%`, "oob"}, {"foobar", `#"o_b#"%`, nil},
		{"abcdefg", `a*#"%#"g*`, "abcdefg"}, {"abcdefg", `a|b#"%#"g`, "bcdef"},
		{"abcdefg", `a#"%#"x|g`, "bcdef"}, {"abcdefg", `a#"%|ab#"g`, "bcdef"},
		{"abcdefg", `a#"%g`, "bcdefg"}, {"abcdefg", `a%g`, "abcdefg"},
		{"ab", `a#"#"b`, ""}, {"abcdefg", `a#"(b_d)#"%`, "bcd"},
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range cases {
				for _, expression := range []string{
					`substring($1 SIMILAR $2 ESCAPE $3)`, `substring($1 FROM $2 FOR $3)`,
					`substring($1,$2,$3)`, `pg_catalog.substring($1,$2,$3)`,
				} {
					similarReferenceRoutes(t, p, b, fixture.Select("SELECT "+expression+" AS piece", []string{"piece"}, []string{"TEXT"}, [][]any{{c.value}}, c.source, c.pattern, "#"))
				}
			}
			for _, args := range [][]any{{nil, "%", "#"}, {"abc", nil, "#"}, {"abc", "%", nil}} {
				similarReferenceRoutes(t, p, b, fixture.Select(`SELECT substring($1 SIMILAR $2 ESCAPE $3) AS piece`, []string{"piece"}, []string{"TEXT"}, [][]any{{nil}}, args...))
			}
		})
	}
}

func TestReferenceSimilarTranslation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, step := range []fixture.Step{
			fixture.Select(`SELECT 'a' SIMILAR TO 'a\', '' SIMILAR TO '\', 'a' SIMILAR TO 'a#' ESCAPE '#', 'a' SIMILAR TO 'aé' ESCAPE 'é'`, []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}),
			fixture.Select(`SELECT substring('a' SIMILAR 'a#' ESCAPE '#'), substring('a' SIMILAR 'aé' ESCAPE 'é')`, []string{"substring", "substring"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}}),
			fixture.Select(`SELECT 'abab' SIMILAR TO '#"(ab)#"#1' ESCAPE '#', substring('abab' SIMILAR '#"(ab)#"#1' ESCAPE '#')`, []string{"?column?", "substring"}, []string{"BOOL", "TEXT"}, [][]any{{true, "ab"}}),
			similarReferenceRegexError(`SELECT 'abab' SIMILAR TO '(ab)\1'`, "invalid backreference number", ""),
			fixture.Select(`SELECT 'a' SIMILAR TO '%?', 'aaa' SIMILAR TO 'a+?', '' SIMILAR TO 'a??', 'a' SIMILAR TO 'a{1,1}?'`, []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}),
			fixture.Select(`SELECT '' SIMILAR TO '(){255}', '' SIMILAR TO '(a?)*', 'a' SIMILAR TO 'a{0,1}', 'a' SIMILAR TO 'a{1}'`, []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}),
			similarReferenceRegexError(`SELECT 'a' SIMILAR TO 'a{256}'`, "invalid repetition count(s)", ""),
			fixture.Select(`SELECT 'a{' SIMILAR TO 'a{'`, []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			similarReferenceRoutes(t, p, b, step)
		}
		// Canonical translation, not independently parsed separator sections: the
		// apparent outer + does not repeat the whole original SQL grouping.
		for _, c := range []struct {
			source, pattern string
			matched         bool
			piece           any
			reason          string
		}{
			{"ab", `(a#"b#")`, true, "b", ""},
			{"", `(a#"b#")`, false, nil, ""},
			{"ab", `(a|#"b#")`, true, "b", ""},
			{"abab", `(a|#"b#")`, false, nil, ""},
			{"ab", `(a#"b#")+`, true, "b", ""},
			{"abab", `(a#"b#")+`, false, nil, ""},
			{"ab", `(a#"b#")#1`, false, nil, ""},
			{"", `#"#"#1`, true, "", ""},
			{"ab", `#"#"#1`, false, nil, ""},
			{"aaa", `#"a#1#"`, false, nil, "invalid backreference number"},
			{"aaa", `(#"a#")+#1`, false, nil, ""},
			{"aaa", `#"a#"#01`, false, nil, ""},
			{"aa\t", `#"a#"#011`, false, nil, ""},
			{"aaa", `#"a#"#10`, false, nil, ""},
			{"", `%#"a+?#"%`, false, nil, ""},
			{"aaa", `%#"a+?#"%`, true, "aaa", ""},
			{"aa\t", `%#"a+?#"%`, true, "aa", ""},
			{"abab", `%#"a+?#"%`, true, "a", ""},
			{"aaa", `%?#"a{1}?#"%`, true, "a", ""},
			{"aaa", `%#"a{1,1}?#"%`, true, "a", ""},
		} {
			query := `SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched, substring($1 SIMILAR $2 ESCAPE '#') AS piece`
			step := fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{c.matched, c.piece}}, c.source, c.pattern)
			if c.reason != "" {
				step = similarReferenceRegexError(query, c.reason, "", c.source, c.pattern)
			}
			similarReferenceRoutes(t, p, b, step)
		}
	}
}
