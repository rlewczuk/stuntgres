package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPGRegexModesAtomsConstraints(t *testing.T) {
	for _, tc := range []struct {
		source, pattern string
		want            bool
	}{
		{"abc", "b", true}, {"abc", "^b", false}, {"", "", true}, {"", `a|`, true}, {"", `|a`, true}, {"", `()`, true},
		{"🙂", `^.$`, true}, {"🙂x", `^.$`, false}, {"a{z", `a{z`, true}, {"aaa", `^a{2,3}$`, true}, {"", `^a?$`, true}, {strings.Repeat("a", 255), `^a{255}$`, true},
		{"5", `\d`, true}, {"d", `(?e)\d`, true}, {"5", `(?e)\d`, false}, {")", `(?e))`, true},
		{"()+?|{}", `(?b)()+?|{}`, true}, {"aaaa", `(?b)^\(aa\)\1$`, true}, {"aaa", `(?b)^a\{3\}$`, true}, {"*a", `(?b)^*a$`, true},
		{"a+b", `(?b)a+b`, true}, {"a^b", `(?b)a^b`, true}, {"a$b", `(?b)a$b`, true}, {"a.b", `***=a.b`, true}, {"axb", `***=a.b`, false}, {"7", `***:\d`, true},
		{"abc", `\Aabc\Z`, true}, {"xabc", `\Aabc\Z`, false}, {" abc!", `\mabc\M`, true}, {"xabc", `\mabc`, false}, {"abc", `\yabc\y`, true}, {"xabcx", `\Yabc\Y`, true},
		{"abc!", `[[:<:]]abc[[:>:]]`, true}, {"_abc_", `[[:<:]]abc[[:>:]]`, false},
		{"ab", `a(?=b)`, true}, {"ac", `a(?!b)`, true}, {"ab", `a(?!b)`, false}, {"aab", `(?<=a+)b`, true}, {"cb", `(?<!a+)b`, true}, {"aab", `(?<!a+)b`, false},
		{"ac", `(?=a(?=b))a`, false}, {"ab", `(?=a(?=b))ab`, true}, {"ab", `(a)(?=b(b)?)(b)\2?`, true},
		{"é", `\u00e9`, true}, {"🙂", `\U0001f642`, true}, {"A", `\x41`, true}, {"A", `\101`, true}, {"A", `\0101`, false}, {"\b1", `\0101`, true},
		{"]", `[]a]`, true}, {"-", `[-a]`, true}, {"-", `[a-]`, true}, {"a", `[^]a]`, false}, {"a", `[[=a=]]`, true}, {"\t", `[[.tab.]]`, true},
		{"]", `[\u005d]`, true}, {"[", `[\133]`, true}, {"5", `[\d]`, true}, {"a", `[\D]`, true}, {"_", `[\w]`, true}, {"!", `[\W]`, true},
		{"ab", "(?x) a # comment\n b", true}, {"a b", `(?x)a\ b`, true}, {"a#b", `(?x)a\#b`, true}, {" ", `(?x)[ ]`, true}, {"a b", `(?xt)a b`, true}, {"ab", `(?tx)a b`, true},
		{"a.b", `(?q)a.b`, true}, {"axb", `(?q)a.b`, false}, {"ab", `a(?#comment)b`, true},
	} {
		t.Run(tc.pattern+tc.source, func(t *testing.T) { pgRegexAssertBool(t, tc.source, tc.pattern, 950, tc.want) })
	}
}
func pgRegexAssertBool(t *testing.T, source, pattern string, provider uint32, want bool) {
	t.Helper()
	p, err := CompilePGRegex(context.Background(), pattern, PGRegexOptions{}, CollationRef{ID: provider})
	if err != nil {
		t.Fatal(pattern, err)
	}
	for _, captures := range []bool{false, true} {
		got, err := p.Match(context.Background(), source, captures)
		if err != nil || got.Matched != want {
			t.Fatal(source, pattern, captures, got, err, want)
		}
		if !captures && (got.Whole != (PGRegexSpan{-1, -1}) || got.Captures != nil) {
			t.Fatalf("BOOL-only demand returned unused spans: whole=%+v captures=%+v", got.Whole, got.Captures)
		}
	}
}
func TestPGRegexNewlineMatrix(t *testing.T) {
	for _, mode := range []string{"", "s", "n", "m", "p", "w"} {
		prefix := ""
		if mode != "" {
			prefix = "(?" + mode + ")"
		}
		stop := mode == "n" || mode == "m" || mode == "p"
		anchors := mode == "n" || mode == "m" || mode == "w"
		for _, pattern := range []string{`a.b`, `a[^x]b`} {
			pgRegexAssertBool(t, "a\nb", prefix+pattern, 950, !stop)
		}
		pgRegexAssertBool(t, "a\nb", prefix+`^b$`, 950, anchors)
		pgRegexAssertBool(t, "a\n", prefix+`^a$`, 950, anchors)
		pgRegexAssertBool(t, "a\nb", prefix+`\Ab\Z`, 950, false)
		for _, pattern := range []string{`\D`, `\W`, `[\D]`, `[\W]`} {
			pgRegexAssertBool(t, "\n", prefix+pattern, 950, true)
		}
		pgRegexAssertBool(t, "\n", prefix+`[^0-9]`, 950, !stop)
	}
	pgRegexAssertBool(t, "a\rb", `(?n)a.b`, 950, true)
	pgRegexAssertBool(t, "a\u2028b", `(?n)a.b`, 950, true)
	pgRegexAssertBool(t, "a\r\nb", `(?n)^a$`, 950, false)
}
func TestPGRegexSimpleCaseProviders(t *testing.T) {
	for _, provider := range []uint32{950, 811, 6411} {
		unicode := provider != 950
		for _, tc := range []struct {
			source, pattern string
			want            bool
		}{
			{"AbC", `(?i)abc`, true}, {"AbC", `(?c)abc`, false}, {"É", `(?i)é`, unicode}, {"é", `(?i)[É]`, unicode}, {"é", `(?i)[[:upper:]]`, unicode}, {"Éé", `(?i)(É)\1`, unicode},
			{"ß", `(?i)SS`, false}, {"ς", `(?i)σ`, false}, {"σ", `(?i)ς`, false}, {"Σσ", `(?i)(Σ)\1`, unicode}, {"Σς", `(?i)(Σ)\1`, false}, {"ς", `(?i)[σ]`, false},
			{"A", `(?i)[^a]`, false}, {"Z", `(?i)[a-z]`, true}, {"字", `(?i)[[:upper:]]`, unicode}, {"字", `(?i)[[:lower:]]`, unicode},
			{"ǅ", `(?i)ǅ`, !unicode}, {"ǅ", `(?i)[ǅ]`, !unicode}, {"ǅ", `(?i)[[=ǅ=]]`, !unicode},
			{"ǅ", `(?i)[ǅ-ǅ]`, true}, {"ǅ", `(?i)[[.ǅ.]]`, true}, {"Ǆ", `(?i)[ǅ]`, unicode}, {"ǆ", `(?i)[ǅ]`, unicode},
			{"Ǆ", `(?i)[ǅ-ǅ]`, unicode}, {"ǆ", `(?i)[ǅ-ǅ]`, unicode},
			{"ς", `(?i)[σ-σ]`, false}, {"σ", `(?i)[ς-ς]`, false},
			{"Σ", `(?i)[σ-σ]`, unicode}, {"Σ", `(?i)[ς-ς]`, unicode},
			{"K", `(?i)[k-k]`, false}, {"K", `(?i)[K-K]`, false},
		} {
			pgRegexAssertBool(t, tc.source, tc.pattern, provider, tc.want)
		}
	}
}
func TestPGRegexCanonicalDiagnostics(t *testing.T) {
	for _, tc := range []struct{ pattern, reason string }{
		{"[", "brackets [] not balanced"}, {"(", "parentheses () not balanced"}, {")", "parentheses () not balanced"}, {"a{2", "braces {} not balanced"}, {"a{2,1}", "invalid repetition count(s)"}, {"a{256}", "invalid repetition count(s)"},
		{"[z-a]", "invalid character range"}, {"[a-c-e]", "invalid character range"}, {"[[:alpha:]-z]", "invalid character range"}, {"[[=a=]-z]", "invalid character range"}, {"[[:bogus:]]", "invalid character class"}, {"[[.ch.]]", "invalid collating element"},
		{`\q`, `invalid escape \ sequence`}, {`[\A]`, `invalid escape \ sequence`}, {`\1`, "invalid backreference number"}, {`(a\1)`, "invalid backreference number"}, {`(a)(?=\1)`, "invalid backreference number"}, {`(?<=(a)\1)b`, "invalid backreference number"},
		{"*a", "quantifier operand invalid"}, {"a**", "quantifier operand invalid"}, {"^*", "quantifier operand invalid"}, {"(?=a)*", "quantifier operand invalid"}, {"(?z)a", "invalid embedded option"}, {"a(?i)b", "quantifier operand invalid"}, {"(?i:a)", "invalid embedded option"}, {"***?a", "invalid regexp (reg version 0.8)"},
	} {
		_, err := CompilePGRegex(context.Background(), tc.pattern, PGRegexOptions{}, CollationRef{ID: 950})
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "2201B" || e.Message != "invalid regular expression: "+tc.reason {
			t.Fatal(tc, err)
		}
	}
}
