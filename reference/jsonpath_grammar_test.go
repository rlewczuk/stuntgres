package reference_test

import (
	"strings"
	"testing"
)

// PG18 released grammar defines admission; the complete pinned tests-1 oracle
// fixes every canonical output below, including numeric precision/scale limits.
func TestReferenceJSONPathGrammar(t *testing.T) {
	cases := []struct{ name, input, output string }{
		{"scalars", `true`, "true"}, {"false", `false`, "false"}, {"null", `null`, "null"}, {"string", `"a'b"`, "\"a'b\""},
		{"quoted_key", `strict $."a b"[0]?(@.x >= 1).y`, "strict $.\"a b\"[0]?(@.\"x\" >= 1).\"y\""},
		{"escaped_key", `$.\u0061`, "$.\"a\""}, {"quoted_variable", `$"a b".x`, "$\"a b\".\"x\""},
		{"keyword_keys", `$.true.false.null.strict.lax.last.to.is.unknown.exists.flag`, "$.\"true\".\"false\".\"null\".\"strict\".\"lax\".\"last\".\"to\".\"is\".\"unknown\".\"exists\".\"flag\""},
		{"wildcards", `$.*[*].**`, "$.*[*].**"}, {"recursive_single", `$.**{0}`, "$.**{0}"},
		{"recursive_last", `$.**{last}`, "$.**{last}"}, {"recursive_reversed", `$.**{last to 5}`, "$.**{last to 5}"},
		{"recursive_range", `$.**{0 to 3}`, "$.**{0 to 3}"},
		{"indices", `$[-1,0.5,$x + 1,last - 1,1 to last]`, "$[-1,0.5,$\"x\" + 1,last - 1,1 to last]"},
		{"last_scope_inherited", `$[$?(@ == last)]`, "$[$?(@ == last)]"},
		{"repeated_filter", `$?(@ > 1)?(@ < 3).a`, "$?(@ > 1)?(@ < 3).\"a\""},
		{"nested_filter", `$?(exists(@.a?(@ == 1)))`, "$?(exists (@.\"a\"?(@ == 1)))"},
		{"comparisons", `$?(@ == 1 || @ != 2 && @ <> 3)`, "$?(@ == 1 || @ != 2 && @ != 3)"},
		{"ordering", `$?(@ < 1 || @ <= 2 || @ > 3 || @ >= 4)`, "$?(((@ < 1 || @ <= 2) || @ > 3) || @ >= 4)"},
		{"not", `$?(!(@ == 1))`, "$?(!(@ == 1))"}, {"unknown", `$?((@ == 1) is unknown)`, "$?((@ == 1) is unknown)"},
		{"exists", `exists($.a)`, "exists ($.\"a\")"}, {"starts_string", `$?(@ starts with "a")`, "$?(@ starts with \"a\")"},
		{"starts_variable", `$?(@ starts with $prefix)`, "$?(@ starts with $\"prefix\")"},
		{"top_predicate", `1 == 1 || 2 == 2 && 3 == 3`, "(1 == 1 || 2 == 2 && 3 == 3)"},
		{"unary_constant", `+ - + 1`, "-1"}, {"unary_expression", `-($ + 1)`, "(-($ + 1))"},
		{"precedence", `($ + 1) * 2 / 3 % 4 - 5`, "(((($ + 1) * 2) / 3) % 4 - 5)"},
		{"no_evaluation", `1 / 0`, "(1 / 0)"}, {"method_parenthesized_number", `(1).type()`, "(1).type()"},
		{"method_fraction", `1.2.type()`, "(1.2).type()"}, {"comments", "/* before */ lax $ /* after */ .a\t\n\r\f[0]", "$.\"a\"[0]"},
		{"raw_controls", "\"a\t\n\r\x01b\"", "\"a\\t\\n\\r\\u0001b\""},
		{"escape_family", `"\b\f\n\r\t\v\x41\u0042\u{43}\/\\\""`, "\"\\b\\f\\n\\r\\t\\u000bABC/\\\\\\\"\""},
		{"unicode_limits", `"\u{10ffff}\uFFFF\uD800\uDC00"`, "\"􏿿￿𐀀\""},
		{"decimal_forms", `.1`, "0.1"}, {"trailing_decimal", `1.`, "1"}, {"exponent", `1_2.3_4e+0_2`, "1234"},
		{"binary", `0b1_0`, "2"}, {"octal", `0o7_0`, "56"}, {"hex", `0xA_b`, "171"},
		{"numeric_precision", strings.Repeat("9", 400), strings.Repeat("9", 400)},
		{"numeric_magnitude", "1e131071", "1" + strings.Repeat("0", 131071)}, {"numeric_scale", "1e-16383", "0." + strings.Repeat("0", 16382) + "1"},
		{"datetime_empty", `$.datetime()`, "$.datetime()"}, {"datetime_template", `$.datetime("YYYY-MM-DD")`, "$.datetime(\"YYYY-MM-DD\")"},
		{"decimal_empty", `$.decimal()`, "$.decimal()"}, {"decimal_precision", `$.decimal(10)`, "$.decimal(10)"},
		{"decimal_signed", `$.decimal(+10,-2)`, "$.decimal(10,-2)"},
	}
	for _, method := range []string{"type", "size", "abs", "floor", "double", "ceiling", "keyvalue", "bigint", "boolean", "date", "integer", "number", "string"} {
		cases = append(cases, struct{ name, input, output string }{"method_" + method, "$." + method + "()", "$." + method + "()"})
	}
	for _, method := range []string{"time", "time_tz", "timestamp", "timestamp_tz"} {
		for _, arg := range []string{"", "0", "99"} {
			cases = append(cases, struct{ name, input, output string }{"method_" + method + "_" + arg, "$." + method + "(" + arg + ")", "$." + method + "(" + arg + ")"})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				for _, prepared := range []bool{false, true} {
					jsonpathReferenceOutput(t, p, b, tc.input, tc.output, prepared)
				}
			}
		})
	}
}
