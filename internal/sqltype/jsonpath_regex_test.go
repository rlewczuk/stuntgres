package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Admission and diagnostic categories are independently grounded in PG18's
// ARE grammar and the pinned public Regex oracle family, not Go's RE2 parser.
func TestJSONPathRegexModesAndSyntax(t *testing.T) {
	valid := []string{
		"", "(?=a)a", "(?!a)b", "(?<=a+)b", "(?<!a)b", "(?:a)", `(a)\1`, `((a))\2\1`,
		"(|a|)", `(a){0}\1`, `\U00110000`, "a*b+c?d{2}e{1,3}f{2,}", "a*?b+?c??d{1,2}?", "a{255}",
		"***=(", "***:(?:a)", `(?b)a\{2\}`, `(?b)\(a\)\1`, "(?b)*a", "(?e)a)", "(?e)(a)", `(?e)\q`,
		"(?q)[(", "(?x) a # comment\n b", "a(?# comment)b", "(?cimnpstw)a", `\A\mword\M\y\Y\Z`,
		`\d\D\s\S\w\W`, `\a\b\B\cA\e\f\n\r\t\v\x41\u0042\U00000043\012`,
		"[]a-]", "[^]a-]", "[[:digit:][:alpha:][:space:]]+", "[[:alnum:][:ascii:][:blank:][:cntrl:][:graph:][:lower:][:print:][:punct:][:upper:][:xdigit:][:word:]]",
		"[[.a.]]", "[[=a=]]", "[a-z0-9]", "[é雪]", `[\d\s\w\x41]`, `(?b)^a.*b$`, `\123`, `\000`,
		"[[:<:]]a[[:>:]]", "(?x)[ #] a", "(?iq)[", "(?=(?!b)a)a", "(^|$)", "a{}", "(?x)a{ 1 , 2 }",
		"[[.NUL.]-[.DEL.]]", "[[.zero.]-[.nine.]]", "[[=newline=]]", "[[.雪.]]", `(?b)^*a`,
	}
	for _, pattern := range valid {
		if _, err := validateJSONPathRegex(context.Background(), pattern, ""); err != nil {
			t.Fatalf("valid %q: %v", pattern, err)
		}
	}
	invalid := []struct{ pattern, suffix string }{
		{"(", "parentheses () not balanced"}, {"[", "brackets [] not balanced"}, {"a{256}", "invalid repetition count(s)"}, {"a{2,1}", "invalid repetition count(s)"},
		{"*a", "quantifier operand invalid"}, {"[z-a]", "invalid character range"}, {"[[:bogus:]]", "invalid character class"},
		{`\q`, `invalid escape \ sequence`}, {`\1`, "invalid backreference number"}, {`(a\1)`, "invalid backreference number"}, {`\1(a)`, "invalid backreference number"},
		{`(a)(?=\1)`, "invalid backreference number"}, {`(?=(a))\1`, "invalid backreference number"}, {"^*", "quantifier operand invalid"}, {"a**", "quantifier operand invalid"},
		{"a\\", `invalid escape \ sequence`}, {"a{1", "braces {} not balanced"}, {"***?a", "invalid regexp (reg version 0.8)"}, {"(?j)a", "invalid embedded option"},
		{"(?i:a)", "invalid embedded option"}, {"a)", "parentheses () not balanced"}, {`(?b)a\)`, "parentheses () not balanced"},
		{"[a-[:digit:]]", "invalid character range"}, {"[[.bogus.]]", "invalid collating element"}, {"[[.a]", "brackets [] not balanced"}, {"[[=a]", "brackets [] not balanced"},
		{`\c`, `invalid escape \ sequence`}, {`\x`, `invalid escape \ sequence`}, {`\u123`, `invalid escape \ sequence`},
		{"[z-a](", "invalid character range"}, {`(\1)[`, "invalid backreference number"}, {"(?=a)*", "quantifier operand invalid"}, {`\m+`, "quantifier operand invalid"},
		{"a*??", "quantifier operand invalid"}, {`[\1]`, `invalid escape \ sequence`}, {"[[:digit]", "brackets [] not balanced"}, {"a(?i)b", "quantifier operand invalid"},
	}
	for _, tc := range invalid {
		_, err := validateJSONPathRegex(context.Background(), tc.pattern, "")
		e, ok := err.(*sqlstate.Error)
		if !ok || e.Code != "2201B" || e.Message != "invalid regular expression: "+tc.suffix || e.Detail != "" || e.Position != "" || e.Context != "" {
			t.Fatalf("%q: %#v want %s", tc.pattern, err, tc.suffix)
		}
	}
	for _, flags := range []string{"q", "iq", "smixq"} {
		bits, err := validateJSONPathRegex(context.Background(), "(", flags)
		if err != nil || bits&16 == 0 {
			t.Fatal("literal flags", flags, bits, err)
		}
	}
	bits, err := validateJSONPathRegex(context.Background(), "pattern", "isim")
	if err != nil || bits != 7 {
		t.Fatal(bits, err)
	}
}

// These syntax-provider boundaries mirror the independent REL_18_0 expectations
// observed in tests-7 on pinned18.0. SQL origin/context and prepare/execute routes
// remain exclusively in TestReferenceJSONPathReviewRegex.
func TestJSONPathRegexReviewBoundaries(t *testing.T) {
	cases := []struct{ name, pattern, suffix string }{
		{"bre_empty_lower_exact", `(?b)a\{\}`, ""},
		{"bre_empty_lower_upper", `(?b)a\{,2\}`, ""},
		{"bre_empty_both", `(?b)a\{,\}`, ""},
		{"bre_explicit_zero", `(?b)a\{0\}`, ""},
		{"bre_empty_lower_zero_upper", `(?b)a\{,0\}`, ""},
		{"bre_empty_lower_max_upper", `(?b)a\{,255\}`, ""},
		{"are_empty_lower_literal", `a{,2}`, ""},
		{"ere_empty_lower_literal", `(?e)a{,2}`, ""},
		{"bre_empty_lower_upper_overflow", `(?b)a\{,256\}`, "invalid repetition count(s)"},
		{"bre_lower_overflow", `(?b)a\{256\}`, "invalid repetition count(s)"},
		{"bre_reversed", `(?b)a\{2,1\}`, "invalid repetition count(s)"},
		{"bre_extra_comma", `(?b)a\{,,\}`, "invalid repetition count(s)"},
		{"bre_nondigit_upper", `(?b)a\{,x\}`, "invalid repetition count(s)"},
		{"bre_unescaped_close", `(?b)a\{,2}`, "invalid repetition count(s)"},
		{"bre_empty_lower_eof", `(?b)a\{`, "braces {} not balanced"},
		{"bre_empty_upper_eof", `(?b)a\{,`, "braces {} not balanced"},
		{"bre_upper_eof", `(?b)a\{,2`, "braces {} not balanced"},
		{"collating_before_bad_escape", `[[.bogus.]\q]`, `invalid escape \ sequence`},
		{"class_before_bad_collating", `[[:bogus:][.bogus.]]`, "invalid character class"},
		{"valid_collating_before_bad_escape", `[[.a.]\q]`, `invalid escape \ sequence`},
		{"equivalence_before_bad_escape", `[[=bogus=]\q]`, `invalid escape \ sequence`},
		{"class_before_bad_escape", `[[:bogus:]\q]`, `invalid escape \ sequence`},
		{"class_before_bad_equivalence", `[[:bogus:][=bogus=]]`, "invalid character class"},
		{"class_before_unclosed_collating", `[[:bogus:][.a]`, "invalid character class"},
		{"collating_before_bad_class", `[[.bogus.][:bogus:]]`, "invalid collating element"},
		{"bad_collating_alone", `[[.bogus.]]`, "invalid collating element"},
		{"bad_class_alone", `[[:bogus:]]`, "invalid character class"},
		{"valid_class_before_bad_collating", `[[:alpha:][.bogus.]]`, "invalid collating element"},
		{"valid_named_neighbors", `[[:alpha:][.a.][=a=]]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateJSONPathRegex(context.Background(), tc.pattern, "")
			if tc.suffix == "" {
				if err != nil {
					t.Fatalf("valid %q: %v", tc.pattern, err)
				}
				return
			}
			e, ok := err.(*sqlstate.Error)
			if !ok || e.Code != "2201B" || e.Message != "invalid regular expression: "+tc.suffix || e.Detail != "" || e.Position != "" || e.Context != "" {
				t.Fatalf("%q: %#v want %s", tc.pattern, err, tc.suffix)
			}
		})
	}
}

func TestJSONPathRegexBoundAdmission(t *testing.T) {
	// Successful validation alone cannot distinguish literal ARE/ERE braces
	// from an incorrectly admitted zero-lower bound. Assert the lexical mode
	// boundary independently of the bound parser.
	for _, tc := range []struct{ pattern, kind string }{
		{`(?b)a\{,2\}`, "bound"},
		{`(?b)a\{\}`, "bound"},
		{`a{,2}`, "plain"},
		{`(?e)a{,2}`, "plain"},
		{`a{}`, "plain"},
		{`(?e)a{}`, "plain"},
		{`a{0,2}`, "bound"},
		{`(?e)a{0,2}`, "bound"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			s := pgRegexScanner{ctx: context.Background(), text: tc.pattern, previous: "empty"}
			if err := s.prefix(); err != nil {
				t.Fatal(err)
			}
			if tok, err := s.next(); err != nil || tok.kind != "plain" || tok.code != 'a' {
				t.Fatalf("initial atom: %#v, %v", tok, err)
			}
			if tok, err := s.next(); err != nil || tok.kind != tc.kind {
				t.Fatalf("bound admission: %#v, %v; want %s", tok, err, tc.kind)
			}
		})
	}
}

// Root observed the independent public threshold/EOF contracts in tests-9.
// Exercise the syntax provider and the pending-token cursor separately from
// SQL source positions, portal contexts and phases in the reference family.
func TestJSONPathRegexBoundStopping(t *testing.T) {
	for _, mode := range []struct{ name, prefix, open, close string }{
		{"are", "", "a{", "}"},
		{"ere", "(?e)", "a{", "}"},
		{"bre", "(?b)", `a\{`, `\}`},
		{"expanded_are", "(?x)", "a{", "}"},
		{"expanded_bre", "(?bx)", `a\{`, `\}`},
	} {
		for _, lower := range []string{"", "1,"} {
			// Omitted lower is zero only in BRE; empty here otherwise
			// denotes an exact lower count, not an omitted-lower bound.
			lowers := []string{lower}
			if lower == "1," && strings.Contains(mode.name, "bre") {
				lowers = append(lowers, ",")
			}
			for _, start := range lowers {
				for _, tc := range []struct{ count, suffix string }{
					{"255", "braces {} not balanced"},
					{"256", "braces {} not balanced"},
					{"2550", "invalid repetition count(s)"},
				} {
					for _, terminated := range []bool{false, true} {
						pattern := mode.prefix + mode.open + start + tc.count
						suffix := tc.suffix
						if terminated {
							pattern += mode.close
							suffix = "invalid repetition count(s)"
							if tc.count == "255" {
								suffix = ""
							}
						}
						t.Run(pattern, func(t *testing.T) {
							_, err := validateJSONPathRegex(context.Background(), pattern, "")
							if suffix == "" {
								if err != nil {
									t.Fatalf("valid %q: %v", pattern, err)
								}
								return
							}
							e, ok := err.(*sqlstate.Error)
							if !ok || e.Code != "2201B" || e.Message != "invalid regular expression: "+suffix || e.Detail != "" || e.Position != "" || e.Context != "" {
								t.Fatalf("%q: %#v want %s", pattern, err, suffix)
							}
						})
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		text     string
		expanded bool
		pending  int
	}{
		{"2550", false, 3},
		{"1,2550", false, 5},
		{",2550", false, 4},
		{"2 5 5 # lookahead\n 0", true, len("2 5 5 # lookahead\n ")},
		{"1,2 5 5 # lookahead\n 0", true, len("1,2 5 5 # lookahead\n ")},
	} {
		s := pgRegexScanner{ctx: context.Background(), text: tc.text, expanded: tc.expanded, mode: pgRegexBRE}
		err := s.bound()
		e, ok := err.(*sqlstate.Error)
		if !ok || e.Code != "2201B" || e.Message != "invalid regular expression: invalid repetition count(s)" || s.pos != tc.pending {
			t.Fatalf("pending digit %q: cursor %d want %d, error %#v", tc.text, s.pos, tc.pending, err)
		}
	}
}

func TestJSONPathRegexPrivateStateAndCancellation(t *testing.T) {
	ctx := context.Background()
	// Capture ordinals are input-local and closed captures are never erased by
	// {0}. Revalidating does not inherit captures or embedded mode switches.
	for range 3 {
		if _, err := validateJSONPathRegex(ctx, `(a){0}\1`, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := validateJSONPathRegex(ctx, `\1`, ""); err == nil {
			t.Fatal("capture state leaked")
		}
		if _, err := validateJSONPathRegex(ctx, `(?e)\q`, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := validateJSONPathRegex(ctx, `\q`, ""); err == nil {
			t.Fatal("mode state leaked")
		}
	}
	for _, pattern := range []string{strings.Repeat("(", 10000) + "a" + strings.Repeat(")", 10000), "[" + strings.Repeat("a", 10000) + "]", "(?x)#" + strings.Repeat("a", 10000), strings.Repeat("a", 10000), "(?x)a{255 #" + strings.Repeat("a", 10000) + "\n0", "(?bx)a\\{,255 #" + strings.Repeat("a", 10000) + "\n0"} {
		c := &groupingCancelContext{Context: ctx, at: 100}
		if _, err := validateJSONPathRegex(c, pattern, ""); !errors.Is(err, context.Canceled) {
			t.Fatal("regex cancellation", err)
		}
	}
	deep := strings.Repeat("(?:", 10000) + "a" + strings.Repeat(")", 10000)
	if _, err := validateJSONPathRegex(ctx, deep, ""); err != nil {
		t.Fatal("deep iterative groups", err)
	}
}
