package reference_test

import (
	"strconv"
	"testing"
)

// PostgreSQL18 manual 9.7.3 and REL_18_0 regc_lex.c/regcomp.c establish
// admission independently; regerrs.h establishes the rejection categories.
func TestReferenceJSONPathRegex(t *testing.T) {
	valid := []struct{ name, pattern, flags, output string }{
		{"empty", "", "", "$?(@ like_regex \"\")"}, {"lookahead", "(?=a)a", "", "$?(@ like_regex \"(?=a)a\")"}, {"negative_lookahead", "(?!a)b", "", "$?(@ like_regex \"(?!a)b\")"},
		{"lookbehind", "(?<=a)b", "", "$?(@ like_regex \"(?<=a)b\")"}, {"negative_lookbehind", "(?<!a)b", "", "$?(@ like_regex \"(?<!a)b\")"},
		{"variable_lookbehind", "(?<=a+)b", "", "$?(@ like_regex \"(?<=a+)b\")"}, {"noncapture", "(?:a)", "", "$?(@ like_regex \"(?:a)\")"},
		{"capture", `(a)\1`, "", "$?(@ like_regex \"(a)\\\\1\")"}, {"nested_capture", `((a))\2\1`, "", "$?(@ like_regex \"((a))\\\\2\\\\1\")"},
		{"empty_branches", "(|a|)", "", "$?(@ like_regex \"(|a|)\")"}, {"zero_quantifier", "(a){0}", "", "$?(@ like_regex \"(a){0}\")"},
		{"zero_capture_reference", `(a){0}\1`, "", "$?(@ like_regex \"(a){0}\\\\1\")"},
		{"hex_scalar_above_unicode", `\U00110000`, "", "$?(@ like_regex \"\\\\U00110000\")"},
		{"quantifiers", "a*b+c?d{2}e{1,3}f{2,}", "", "$?(@ like_regex \"a*b+c?d{2}e{1,3}f{2,}\")"}, {"nongreedy", "a*?b+?c??d{1,2}?", "", "$?(@ like_regex \"a*?b+?c??d{1,2}?\")"},
		{"max_bound", "a{255}", "", "$?(@ like_regex \"a{255}\")"}, {"literal_director", "***=(", "", "$?(@ like_regex \"***=(\")"},
		{"are_director", "***:(?:a)", "", "$?(@ like_regex \"***:(?:a)\")"}, {"bre", `(?b)a\{2\}`, "", "$?(@ like_regex \"(?b)a\\\\{2\\\\}\")"},
		{"bre_capture", `(?b)\(a\)\1`, "", "$?(@ like_regex \"(?b)\\\\(a\\\\)\\\\1\")"}, {"bre_star", "(?b)*a", "", "$?(@ like_regex \"(?b)*a\")"},
		{"ere_close_literal", "(?e)a)", "", "$?(@ like_regex \"(?e)a)\")"}, {"ere_capture", "(?e)(a)", "", "$?(@ like_regex \"(?e)(a)\")"},
		{"quoted_option", "(?q)[(", "", "$?(@ like_regex \"(?q)[(\")"}, {"expanded", "(?x) a # comment\n b", "", "$?(@ like_regex \"(?x) a # comment\\n b\")"},
		{"lexical_comment", "a(?# comment)b", "", "$?(@ like_regex \"a(?# comment)b\")"}, {"newline_options", "(?n)^a$", "", "$?(@ like_regex \"(?n)^a$\")"},
		{"option_family", "(?cimnpstw)a", "", "$?(@ like_regex \"(?cimnpstw)a\")"},
		{"anchors", `\A\mword\M\y\Y\Z`, "", "$?(@ like_regex \"\\\\A\\\\mword\\\\M\\\\y\\\\Y\\\\Z\")"},
		{"classes", `\d\D\s\S\w\W`, "", "$?(@ like_regex \"\\\\d\\\\D\\\\s\\\\S\\\\w\\\\W\")"},
		{"character_escapes", `\a\b\B\cA\e\f\n\r\t\v\x41\u0042\U00000043\012`, "", "$?(@ like_regex \"\\\\a\\\\b\\\\B\\\\cA\\\\e\\\\f\\\\n\\\\r\\\\t\\\\v\\\\x41\\\\u0042\\\\U00000043\\\\012\")"},
		{"bracket", "[]a-]", "", "$?(@ like_regex \"[]a-]\")"}, {"negated_bracket", "[^]a-]", "", "$?(@ like_regex \"[^]a-]\")"},
		{"class", "[[:digit:][:alpha:][:space:]]+", "", "$?(@ like_regex \"[[:digit:][:alpha:][:space:]]+\")"},
		{"all_classes", "[[:alnum:][:ascii:][:blank:][:cntrl:][:graph:][:lower:][:print:][:punct:][:upper:][:xdigit:]]", "", "$?(@ like_regex \"[[:alnum:][:ascii:][:blank:][:cntrl:][:graph:][:lower:][:print:][:punct:][:upper:][:xdigit:]]\")"},
		{"collating", "[[.a.]]", "", "$?(@ like_regex \"[[.a.]]\")"}, {"equivalence", "[[=a=]]", "", "$?(@ like_regex \"[[=a=]]\")"},
		{"range", "[a-z0-9]", "i", "$?(@ like_regex \"[a-z0-9]\" flag \"i\")"}, {"unicode", "[é雪]", "", "$?(@ like_regex \"[é雪]\")"},
		{"bracket_escape", `[\d\s\w\x41]`, "", "$?(@ like_regex \"[\\\\d\\\\s\\\\w\\\\x41]\")"},
		{"literal_flag", "(", "q", "$?(@ like_regex \"(\" flag \"q\")"}, {"literal_all_flags", "(", "smixq", "$?(@ like_regex \"(\" flag \"ismxq\")"},
		{"duplicate_flags", "pattern", "isim", "$?(@ like_regex \"pattern\" flag \"ism\")"}, {"empty_flags", "pattern", "", "$?(@ like_regex \"pattern\")"},
		{"ere_plain_escape", `(?e)\q`, "", "$?(@ like_regex \"(?e)\\\\q\")"},
		{"bre_anchors", `(?b)^a.*b$`, "", "$?(@ like_regex \"(?b)^a.*b$\")"},
		{"octal_nonbackref", `\123`, "", "$?(@ like_regex \"\\\\123\")"},
		{"zero_octal", `\000`, "", "$?(@ like_regex \"\\\\000\")"},
		{"word_brackets", `[[:<:]]a[[:>:]]`, "", "$?(@ like_regex \"[[:<:]]a[[:>:]]\")"},
		{"expanded_bracket", "(?x)[ #] a", "", "$?(@ like_regex \"(?x)[ #] a\")"},
		{"quoted_embedded_flags", "(?iq)[", "", "$?(@ like_regex \"(?iq)[\")"},
		{"nested_lookaround", "(?=(?!b)a)a", "", "$?(@ like_regex \"(?=(?!b)a)a\")"},
		{"constraint_alternative", "(^|$)", "", "$?(@ like_regex \"(^|$)\")"},
		{"literal_brace", "a{}", "", "$?(@ like_regex \"a{}\")"},
	}
	invalid := []struct{ name, pattern, flags, suffix string }{
		{"parenthesis", "(", "", "parentheses () not balanced"},
		{"bracket", "[", "", "brackets [] not balanced"},
		{"max_bound", "a{256}", "", "invalid repetition count(s)"},
		{"reversed_bound", "a{2,1}", "", "invalid repetition count(s)"},
		{"operand", "*a", "", "quantifier operand invalid"},
		{"range", "[z-a]", "", "invalid character range"},
		{"class", "[[:bogus:]]", "", "invalid character class"},
		{"escape", `\q`, "", `invalid escape \ sequence`},
		{"backref", `\1`, "", "invalid backreference number"},
		{"self_reference", `(a\1)`, "", "invalid backreference number"},
		{"forward_reference", `\1(a)`, "", "invalid backreference number"},
		{"lookaround_reference", `(a)(?=\1)`, "", "invalid backreference number"},
		{"lookaround_capture", `(?=(a))\1`, "", "invalid backreference number"},
		{"constraint_quantifier", "^*", "", "quantifier operand invalid"},
		{"repeated_quantifier", "a**", "", "quantifier operand invalid"},
		{"trailing_escape", "a\\", "", "invalid escape \\ sequence"},
		{"bound_eof", "a{1", "", "braces {} not balanced"},
		{"director", "***?a", "", "invalid regexp (reg version 0.8)"}, {"option", "(?j)a", "", "invalid embedded option"},
		{"scoped_option", "(?i:a)", "", "invalid embedded option"}, {"are_close", "a)", "", "parentheses () not balanced"},
		{"bre_close", `(?b)a\)`, "", "parentheses () not balanced"},
		{"class_range", "[a-[:digit:]]", "", "invalid character range"},
		{"collation", "[[.bogus.]]", "", "invalid collating element"}, {"collation_eof", "[[.a]", "", "brackets [] not balanced"},
		{"equivalence_eof", "[[=a]", "", "brackets [] not balanced"},
		{"bad_control", `\c`, "", "invalid escape \\ sequence"}, {"bad_hex", `\x`, "", "invalid escape \\ sequence"},
		{"bad_unicode", `\u123`, "", "invalid escape \\ sequence"},
		{"first_error", "[z-a](", "", "invalid character range"}, {"capture_priority", `(\1)[`, "", "invalid backreference number"},
		{"lookahead_quantifier", "(?=a)*", "", "quantifier operand invalid"},
		{"word_constraint_quantifier", `\m+`, "", "quantifier operand invalid"},
		{"multiple_nongreedy", "a*??", "", "quantifier operand invalid"},
		{"bracket_backref", `[\1]`, "", "invalid escape \\ sequence"},
		{"named_class_eof", "[[:digit]", "", "brackets [] not balanced"},
		{"bad_embedded_position", "a(?i)b", "", "quantifier operand invalid"},
	}
	for _, tc := range valid {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			input := "$?(@ like_regex " + strconv.Quote(tc.pattern)
			if tc.flags != "" {
				input += " flag " + strconv.Quote(tc.flags)
			}
			input += ")"
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					jsonpathReferenceOutput(t, p, b, input, tc.output, prepared)
				}
			}
		})
	}
	for _, tc := range invalid {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			input := "$?(@ like_regex " + strconv.Quote(tc.pattern) + ")"
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201B", Message: "invalid regular expression: " + tc.suffix}
					jsonpathReferenceFailureRoutes(t, p, b, input, want, prepared)
				}
			}
		})
	}
}

// Review F1/F2 expectations come from REL_18_0 regc_lex.c (L_BBND),
// regcomp.c (scannum, parseqatom, scanplain, brackpart) and regerrs.h.
// Root observed these independent expectations on pinned PostgreSQL 18.0.
func TestReferenceJSONPathReviewRegex(t *testing.T) {
	cases := []struct{ name, pattern, output, suffix string }{
		{"bre_empty_lower_exact", `(?b)a\{\}`, `$?(@ like_regex "(?b)a\\{\\}")`, ""},
		{"bre_empty_lower_upper", `(?b)a\{,2\}`, `$?(@ like_regex "(?b)a\\{,2\\}")`, ""},
		{"bre_empty_both", `(?b)a\{,\}`, `$?(@ like_regex "(?b)a\\{,\\}")`, ""},
		{"bre_explicit_zero", `(?b)a\{0\}`, `$?(@ like_regex "(?b)a\\{0\\}")`, ""},
		{"bre_empty_lower_zero_upper", `(?b)a\{,0\}`, `$?(@ like_regex "(?b)a\\{,0\\}")`, ""},
		{"bre_empty_lower_max_upper", `(?b)a\{,255\}`, `$?(@ like_regex "(?b)a\\{,255\\}")`, ""},
		{"are_empty_lower_literal", `a{,2}`, `$?(@ like_regex "a{,2}")`, ""},
		{"ere_empty_lower_literal", `(?e)a{,2}`, `$?(@ like_regex "(?e)a{,2}")`, ""},
		{"bre_empty_lower_upper_overflow", `(?b)a\{,256\}`, "", "invalid repetition count(s)"},
		{"bre_lower_overflow", `(?b)a\{256\}`, "", "invalid repetition count(s)"},
		{"bre_reversed", `(?b)a\{2,1\}`, "", "invalid repetition count(s)"},
		{"bre_extra_comma", `(?b)a\{,,\}`, "", "invalid repetition count(s)"},
		{"bre_nondigit_upper", `(?b)a\{,x\}`, "", "invalid repetition count(s)"},
		{"bre_unescaped_close", `(?b)a\{,2}`, "", "invalid repetition count(s)"},
		{"bre_empty_lower_eof", `(?b)a\{`, "", "braces {} not balanced"},
		{"bre_empty_upper_eof", `(?b)a\{,`, "", "braces {} not balanced"},
		{"bre_upper_eof", `(?b)a\{,2`, "", "braces {} not balanced"},
		{"collating_before_bad_escape", `[[.bogus.]\q]`, "", `invalid escape \ sequence`},
		{"class_before_bad_collating", `[[:bogus:][.bogus.]]`, "", "invalid character class"},
		{"valid_collating_before_bad_escape", `[[.a.]\q]`, "", `invalid escape \ sequence`},
		{"equivalence_before_bad_escape", `[[=bogus=]\q]`, "", `invalid escape \ sequence`},
		{"class_before_bad_escape", `[[:bogus:]\q]`, "", `invalid escape \ sequence`},
		{"class_before_bad_equivalence", `[[:bogus:][=bogus=]]`, "", "invalid character class"},
		{"class_before_unclosed_collating", `[[:bogus:][.a]`, "", "invalid character class"},
		{"collating_before_bad_class", `[[.bogus.][:bogus:]]`, "", "invalid collating element"},
		{"bad_collating_alone", `[[.bogus.]]`, "", "invalid collating element"},
		{"bad_class_alone", `[[:bogus:]]`, "", "invalid character class"},
		{"valid_class_before_bad_collating", `[[:alpha:][.bogus.]]`, "", "invalid collating element"},
		{"valid_named_neighbors", `[[:alpha:][.a.][=a=]]`, `$?(@ like_regex "[[:alpha:][.a.][=a=]]")`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			input := "$?(@ like_regex " + strconv.Quote(tc.pattern) + ")"
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					if tc.suffix == "" {
						jsonpathReferenceOutput(t, p, b, input, tc.output, prepared)
					} else {
						want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201B", Message: "invalid regular expression: " + tc.suffix}
						jsonpathReferenceFailureRoutes(t, p, b, input, want, prepared)
					}
				}
			}
		})
	}
}

// Review R2-F1: REL_18_0 regcomp.c scannum stops at n >= DUPMAX,
// after acquiring one more bound token. A pending digit is BADBR without
// requesting EOF; acquiring EOF after 255 or 256 instead reports EBRACE.
// Root observed these independent expectations on pinned PostgreSQL 18.0.
func TestReferenceJSONPathReviewBound(t *testing.T) {
	cases := []struct{ name, pattern, output, suffix string }{
		{"are_lower_255_eof", `a{255`, "", "braces {} not balanced"},
		{"are_lower_256_eof", `a{256`, "", "braces {} not balanced"},
		{"are_lower_2550_eof", `a{2550`, "", "invalid repetition count(s)"},
		{"are_lower_255_closed", `a{255}`, `$?(@ like_regex "a{255}")`, ""},
		{"are_lower_256_closed", `a{256}`, "", "invalid repetition count(s)"},
		{"are_lower_2550_closed", `a{2550}`, "", "invalid repetition count(s)"},
		{"are_upper_255_eof", `a{1,255`, "", "braces {} not balanced"},
		{"are_upper_256_eof", `a{1,256`, "", "braces {} not balanced"},
		{"are_upper_2550_eof", `a{1,2550`, "", "invalid repetition count(s)"},
		{"are_upper_255_closed", `a{1,255}`, `$?(@ like_regex "a{1,255}")`, ""},
		{"are_upper_256_closed", `a{1,256}`, "", "invalid repetition count(s)"},
		{"are_upper_2550_closed", `a{1,2550}`, "", "invalid repetition count(s)"},
		{"bre_lower_255_eof", `(?b)a\{255`, "", "braces {} not balanced"},
		{"bre_lower_256_eof", `(?b)a\{256`, "", "braces {} not balanced"},
		{"bre_lower_2550_eof", `(?b)a\{2550`, "", "invalid repetition count(s)"},
		{"bre_lower_255_closed", `(?b)a\{255\}`, `$?(@ like_regex "(?b)a\\{255\\}")`, ""},
		{"bre_lower_256_closed", `(?b)a\{256\}`, "", "invalid repetition count(s)"},
		{"bre_lower_2550_closed", `(?b)a\{2550\}`, "", "invalid repetition count(s)"},
		{"bre_omitted_lower_upper_255_eof", `(?b)a\{,255`, "", "braces {} not balanced"},
		{"bre_omitted_lower_upper_256_eof", `(?b)a\{,256`, "", "braces {} not balanced"},
		{"bre_omitted_lower_upper_2550_eof", `(?b)a\{,2550`, "", "invalid repetition count(s)"},
		{"bre_omitted_lower_upper_255_closed", `(?b)a\{,255\}`, `$?(@ like_regex "(?b)a\\{,255\\}")`, ""},
		{"bre_omitted_lower_upper_256_closed", `(?b)a\{,256\}`, "", "invalid repetition count(s)"},
		{"bre_omitted_lower_upper_2550_closed", `(?b)a\{,2550\}`, "", "invalid repetition count(s)"},
		{"bre_explicit_lower_upper_255_eof", `(?b)a\{1,255`, "", "braces {} not balanced"},
		{"bre_explicit_lower_upper_256_eof", `(?b)a\{1,256`, "", "braces {} not balanced"},
		{"bre_explicit_lower_upper_2550_eof", `(?b)a\{1,2550`, "", "invalid repetition count(s)"},
		{"bre_explicit_lower_upper_255_closed", `(?b)a\{1,255\}`, `$?(@ like_regex "(?b)a\\{1,255\\}")`, ""},
		{"bre_explicit_lower_upper_256_closed", `(?b)a\{1,256\}`, "", "invalid repetition count(s)"},
		{"bre_explicit_lower_upper_2550_closed", `(?b)a\{1,2550\}`, "", "invalid repetition count(s)"},
		{"ere_lower_255_eof", `(?e)a{255`, "", "braces {} not balanced"},
		{"ere_lower_256_eof", `(?e)a{256`, "", "braces {} not balanced"},
		{"ere_lower_2550_eof", `(?e)a{2550`, "", "invalid repetition count(s)"},
		{"ere_upper_2550_eof", `(?e)a{1,2550`, "", "invalid repetition count(s)"},
		{"ere_lower_255_closed", `(?e)a{255}`, `$?(@ like_regex "(?e)a{255}")`, ""},
		{"ere_upper_256_closed", `(?e)a{1,256}`, "", "invalid repetition count(s)"},
		{"expanded_lower_255_eof", "(?x)a{2 5 5 # end", "", "braces {} not balanced"},
		{"expanded_lower_256_eof", "(?x)a{2 5 6 # end", "", "braces {} not balanced"},
		{"expanded_lower_2550_eof", "(?x)a{2 5 5 # next\n 0", "", "invalid repetition count(s)"},
		{"expanded_upper_2550_eof", "(?x)a{1,2 5 5 # next\n 0", "", "invalid repetition count(s)"},
		{"expanded_lower_255_closed", "(?x)a{2 5 5 # close\n }", `$?(@ like_regex "(?x)a{2 5 5 # close\n }")`, ""},
		{"expanded_upper_256_closed", "(?x)a{1,2 5 6 }", "", "invalid repetition count(s)"},
		{"expanded_bre_lower_2550_eof", "(?bx)a\\{2 5 5 # next\n 0", "", "invalid repetition count(s)"},
		{"expanded_bre_omitted_lower_upper_2550_eof", "(?bx)a\\{,2 5 5 # next\n 0", "", "invalid repetition count(s)"},
		{"expanded_bre_omitted_lower_upper_255_closed", "(?bx)a\\{,2 5 5 # close\n \\}", `$?(@ like_regex "(?bx)a\\{,2 5 5 # close\n \\}")`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			input := "$?(@ like_regex " + strconv.Quote(tc.pattern) + ")"
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					if tc.suffix == "" {
						jsonpathReferenceOutput(t, p, b, input, tc.output, prepared)
					} else {
						want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201B", Message: "invalid regular expression: " + tc.suffix}
						jsonpathReferenceFailureRoutes(t, p, b, input, want, prepared)
					}
				}
			}
		})
	}
}
