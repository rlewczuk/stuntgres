package reference_test

import (
	"testing"
)

type jsonpathReferenceFailure struct{ name, input, code, message, detail string }

// Released diagnostics plus pinned PostgreSQL 18.0 portal-context contracts.
var jsonpathReferenceFailures = []jsonpathReferenceFailure{
	{"empty", "", "22P02", `invalid input syntax for type jsonpath: ""`, ""},
	{"last", "last", "42601", "LAST is allowed only in array subscripts", ""},
	{"last_filter", "$ ? (last > 0)", "42601", "LAST is allowed only in array subscripts", ""},
	{"current_root", "@ + 1", "42601", "@ is not allowed in root expressions", ""},
	{"numeric_method", "1.type()", "42601", `trailing junk after numeric literal at or near "1.t" of jsonpath input`, ""},
	{"numeric_junk", "1a", "42601", `trailing junk after numeric literal at or near "1a" of jsonpath input`, ""},
	{"radix_junk", "0b0x", "42601", "syntax error at end of jsonpath input", ""},
	{"unicode_hex", `"\u000g"`, "42601", `invalid Unicode escape sequence at or near "\u000" of jsonpath input`, ""},
	{"unicode_short", `"\u"`, "42601", `invalid Unicode escape sequence at or near "\u" of jsonpath input`, ""},
	{"unicode_zero", `"\u0000"`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`},
	{"high_high", `"\ud83d\ud83d"`, "22P02", "invalid input syntax for type jsonpath", "Unicode high surrogate must not follow a high surrogate."},
	{"low_high", `"\ude04\ud83d"`, "22P02", "invalid input syntax for type jsonpath", "Unicode low surrogate must follow a high surrogate."},
	{"high_text", `"\ud83dX"`, "22P02", "invalid input syntax for type jsonpath", "Unicode low surrogate must follow a high surrogate."},
	{"regex_parenthesis", `$ ? (@ like_regex "(invalid pattern")`, "2201B", "invalid regular expression: parentheses () not balanced", ""},
	{"regex_flag", `$ ? (@ like_regex "pattern" flag "a")`, "42601", "invalid input syntax for type jsonpath", `Unrecognized flag character "a" in LIKE_REGEX predicate.`},
	{"regex_expanded", `$ ? (@ like_regex "pattern" flag "xsms")`, "0A000", `XQuery "x" flag (expanded regular expressions) is not implemented`, ""},
}

func jsonpathReferenceDiagnostic(tc jsonpathReferenceFailure) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
}

func TestReferenceJSONPathDiagnostics(t *testing.T) {
	for _, tc := range jsonpathReferenceFailures {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
						want := jsonpathReferenceDiagnostic(tc)
						want.Position = "8"
						if prefix != "SELECT " {
							want.Position = "14"
						}
						s := referenceErrorDiagnostic(prefix+jsonReferenceQuote(tc.input)+"::jsonpath", want)
						s.selectRows, s.errorStage = true, "execute"
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					}
					jsonpathReferenceBindDiagnostic(t, p, b, "SELECT $1::jsonpath", tc.input, jsonpathReferenceDiagnostic(tc), prepared)
				}
			}
		})
	}
}

func TestReferenceJSONPathDiagnosticProbes(t *testing.T) {
	cases := []jsonpathReferenceFailure{
		{"unfinished_index", "$[", "42601", "syntax error at end of jsonpath input", ""}, {"mode_only", "strict", "42601", "syntax error at end of jsonpath input", ""},
		{"identifier", "abc", "42601", "syntax error at end of jsonpath input", ""}, {"single_equal", "$ ? (@ = 1)", "42601", "syntax error at or near \"=\" of jsonpath input", ""},
		{"empty_index", "$[]", "42601", "syntax error at or near \"]\" of jsonpath input", ""}, {"first_comma", "$[,1]", "42601", "syntax error at or near \",\" of jsonpath input", ""},
		{"last_comma", "$[1,]", "42601", "syntax error at or near \"]\" of jsonpath input", ""}, {"missing_comma", "$[1 2]", "42601", "syntax error at or near \"2\" of jsonpath input", ""},
		{"scalar_filter", "$?(true)", "42601", "syntax error at or near \")\" of jsonpath input", ""}, {"filter_eof", "$?(@ == 1", "42601", "syntax error at end of jsonpath input", ""},
		{"unknown_method", "$.wat()", "42601", "syntax error at or near \"(\" of jsonpath input", ""}, {"pg20_method", "$.lower()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"time_signed", "$.time(-1)", "42601", "syntax error at or near \"-\" of jsonpath input", ""}, {"time_numeric", "$.time(1.0)", "42601", "syntax error at or near \"1.0\" of jsonpath input", ""},
		{"datetime_number", "$.datetime(1)", "42601", "syntax error at or near \"1\" of jsonpath input", ""}, {"decimal_excess", "$.decimal(1,2,3)", "42601", "invalid input syntax for type jsonpath", ".decimal() can only have an optional precision[,scale]."},
		{"unterminated_quote", `"abc`, "42601", "unterminated quoted string at end of jsonpath input", ""}, {"unterminated_comment", "$ /* abc", "42601", "unexpected end of comment at end of jsonpath input", ""},
		{"trailing_escape", `"abc\`, "42601", "unexpected end after backslash at or near \"\\\" of jsonpath input", ""}, {"bad_hex", `"\x0g"`, "42601", "invalid hexadecimal character sequence at or near \"\\x0\" of jsonpath input", ""},
		{"empty_unicode_brace", `"\u{}"`, "42601", "invalid Unicode escape sequence at or near \"\\u{\" of jsonpath input", ""}, {"unicode_range", `"\u{110000}"`, "42601", "invalid Unicode code point", ""},
		{"prefix_separator", "0x_FF", "42601", "syntax error at end of jsonpath input", ""}, {"leading_zero", "01", "42601", "trailing junk after numeric literal at or near \"01\" of jsonpath input", ""},
		{"double_separator", "1__0", "42601", "syntax error at end of jsonpath input", ""}, {"trailing_separator", "1_", "42601", "trailing junk after numeric literal at or near \"1_\" of jsonpath input", ""},
		{"numeric_overflow", "1e131072", "22003", "value overflows numeric format", ""}, {"numeric_scale", "1e-16384", "22003", "value overflows numeric format", ""},
		{"recursive_overflow", "$.**{2147483648}", "22003", "value \"2147483648\" is out of range for type integer", ""},
		{"scope_regex_priority", `@ ? (@ like_regex "(")`, "2201B", "invalid regular expression: parentheses () not balanced", ""},
		{"flag_pattern_priority", `$?(@ like_regex "(" flag "xa")`, "42601", "invalid input syntax for type jsonpath", "Unrecognized flag character \"a\" in LIKE_REGEX predicate."},
		{"expanded_pattern_priority", `$?(@ like_regex "(" flag "x")`, "0A000", "XQuery \"x\" flag (expanded regular expressions) is not implemented", ""},
		{"misplaced_mode", "$ lax", "42601", "syntax error at end of jsonpath input", ""},
		{"method_argument", "$.size(1)", "42601", "syntax error at or near \"1\" of jsonpath input", ""},
		{"time_variable", "$.time($p)", "42601", "syntax error at or near \"$p\" of jsonpath input", ""},
		{"decimal_fraction", "$.decimal(1.0)", "42601", "syntax error at or near \"1.0\" of jsonpath input", ""},
		{"decimal_variable", "$.decimal($p)", "42601", "syntax error at or near \"$p\" of jsonpath input", ""},
		{"recursive_signed", "$.**{-1}", "42601", "syntax error at or near \"-\" of jsonpath input", ""},
		{"recursive_fraction", "$.**{1.0}", "42601", "syntax error at or near \"1.0\" of jsonpath input", ""},
		{"pg20_upper", "$.upper()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_replace", "$.replace()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_ltrim", "$.ltrim()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_rtrim", "$.rtrim()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_btrim", "$.btrim()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_initcap", "$.initcap()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
		{"pg20_split_part", "$.split_part()", "42601", "syntax error at or near \"(\" of jsonpath input", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					jsonpathReferenceFailureRoutes(t, p, b, tc.input, jsonpathReferenceDiagnostic(tc), prepared)
				}
			}
		})
	}
}

// Literal conversion fails during Prepare; inferred bind conversion fails only
// during execution and carries the measured unnamed-portal parameter context.
func jsonpathReferenceFailureRoutes(t *testing.T, p *referencePair, b referenceBackend, input string, want referenceDiagnostic, prepared bool) {
	t.Helper()
	literal := want
	literal.Position = "8"
	s := referenceErrorDiagnostic("SELECT "+jsonReferenceQuote(input)+"::jsonpath AS v", literal)
	s.selectRows, s.errorStage = true, "execute"
	if prepared {
		s = referencePrepared(s)
		s.errorStage = "prepare"
	}
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	jsonpathReferenceBindDiagnostic(t, p, b, "SELECT $1::jsonpath AS v", input, want, prepared)
}

func jsonpathReferenceBindDiagnostic(t *testing.T, p *referencePair, b referenceBackend, query, input string, want referenceDiagnostic, prepared bool) {
	t.Helper()
	want.Context = "unnamed portal parameter $1 = '...'"
	if input == "" {
		want.Context = "unnamed portal parameter $1 = ''"
	}
	s := referenceErrorDiagnostic(query, want, input)
	s.selectRows, s.errorStage = true, "execute"
	if prepared {
		s = referencePrepared(s)
	}
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
}
