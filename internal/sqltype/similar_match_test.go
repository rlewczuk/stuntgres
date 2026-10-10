package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func similarTestProgram(t *testing.T, pattern, escape string, collation uint32) *SimilarProgram {
	t.Helper()
	canonical, err := TranslateSimilar(context.Background(), pattern, escape)
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompileSimilar(context.Background(), canonical, CollationRef{ID: collation})
	if err != nil {
		t.Fatalf("compile %q (%q): %v", pattern, canonical, err)
	}
	return p
}
func TestSimilarMatchRegularGrammar(t *testing.T) {
	for _, tc := range []struct {
		source, pattern string
		want            bool
	}{
		{"abc", "abc", true}, {"abc", "b", false}, {"abc", "%(b|d)%", true}, {"abc", "(b|c)%", false},
		{"", "%", true}, {"é", "_", true}, {"é", "_", false}, {"é", "__", false}, {"\n", "_", true}, {"a\n", "a", false},
		{"a.c", "a.c", true}, {"abc", "a.c", false}, {"^a$", "^a$", true},
		{"abab", "(ab){2}", true}, {"aaa", "a{2,3}", true}, {"b", "a*b", true}, {"ab", "a+b", true}, {"b", "a?b", true},
		{"", "(){255}", true}, {"", "(a?)*", true}, {"aaaa", "(a?)*", true}, {"", "(a|)*", true},
		{strings.Repeat("a", 255), "a{255}", true}, {"a{", "a{", true},
		{"aaa", "a+?", true}, {"aaa", "%?", true}, {"a", "a??", true}, {"a", "a{1,1}?", true},
		{"5", `\d`, true}, {"a", `\D`, true}, {" ", `\s`, true}, {"_", `\w`, true}, {"-abc-", `%\mabc\M%`, true}, {"xabcy", `%\mabc\M%`, false},
		{"abc", `\Aabc\Z`, true}, {"abc", `\yabc\y`, true}, {"xabcx", `x\Yabc\Yx`, true},
		{"\b", `\b`, true}, {"\\", `\B`, true}, {"\x07", `\a`, true}, {"\x1b", `\e`, true}, {"\x01", `\cA`, true},
		{"é", `\u00E9`, true}, {"界", `\U0000754C`, true}, {"A", `\x41`, true}, {"A", `\101`, true}, {"\t", `\011`, true},
		{"1", `\01` + "1", false}, {"", `\uD800`, false}, {"", `\U00110000`, false},
		{"a", `[[:<:]]a[[:>:]]`, true}, {"a", `[[:ascii:]]`, true}, {"_", `[[:word:]]`, true},
		{"]", `[]a]`, true}, {"b", `[^]a]`, true}, {"^", `[^^]`, false}, {"-", `[-a]`, true}, {"-", `[a-]`, true},
		{"b", `[a-c]`, true}, {"a", `[[.a.]]`, true}, {"a", `[[=a=]]`, true}, {"\t", `[[.tab.]]`, true}, {"%_", `[%][_]`, true},
	} {
		p := similarTestProgram(t, tc.pattern, "\\", 950)
		got, start, end, err := p.Match(context.Background(), tc.source)
		if err != nil || got != tc.want || start != -1 || end != -1 {
			t.Fatalf("%q / %q: %v %d %d %v want %v", tc.source, tc.pattern, got, start, end, err, tc.want)
		}
	}
}
func TestSimilarMatchCapturePreferences(t *testing.T) {
	for _, tc := range []struct {
		source, pattern, piece string
		want                   bool
	}{
		{"foobar", `%#"o_b#"%`, "oob", true},
		{"foobar", `#"o_b#"%`, "", false},
		{"abcdefg", `a*#"%#"g*`, "abcdefg", true},
		{"abcdefg", `a|b#"%#"g`, "bcdef", true},
		{"abcdefg", `a#"%#"x|g`, "bcdef", true},
		{"abcdefg", `a#"%|ab#"g`, "bcdef", true},
		{"abcdefg", `a#"%g`, "bcdefg", true},
		{"ab", `a#"#"b`, "", true},
		{"é界z", `_#"_#"_`, "界", true},
		{"abab", `#"(ab)#"#1`, "ab", true},
		{"", `#"#"#1`, "", true},
		{"ab", `(a#"b#")`, "b", true},
		{"ab", `(a|#"b#")`, "b", true},
		{"ab", `(a#"b#")+`, "b", true},
		{"abab", `(a#"b#")+`, "", false},
		{"abab", `(a#"b#")#1`, "", false},
		{"aaa", `(#"a#")+#1`, "", false},
		{"aaa", `%#"a+?#"%`, "aaa", true},
		{"aa\t", `%#"a+?#"%`, "aa", true},
		{"aaa", `%?#"a{1}?#"%`, "a", true},
		{"aaa", `%#"a{1,1}?#"%`, "a", true},
		// A longest capture of three bytes cannot satisfy its backreference.
		// Capture-dependent visited keys must retain the feasible two-byte path.
		{"aaaa", `#"a+#"#1`, "aa", true},
		{"aaaaaa", `#"a+#"#1`, "aaa", true},
		{"a\x01", `#"a#"#01`, "a", true},
		{"a\t", `#"a#"#011`, "a", true},
		{"a\b", `#"a#"#10`, "a", true},
		{"éé", `#"é#"#1`, "é", true},
	} {
		p := similarTestProgram(t, tc.pattern, "#", 950)
		yes, start, end, err := p.Match(context.Background(), tc.source)
		if err != nil || yes != tc.want {
			t.Fatalf("%q / %q: %v %d %d %v", tc.source, tc.pattern, yes, start, end, err)
		}
		if yes && (start < 0 || end < start || end > len(tc.source) || tc.source[start:end] != tc.piece) {
			t.Fatalf("%q / %q capture %d:%d want %q", tc.source, tc.pattern, start, end, tc.piece)
		}
		// Reuse must not retain winning captures or input from an earlier call.
		if _, _, _, err = p.Match(context.Background(), "impossible"); err != nil {
			t.Fatal(err)
		}
		yes, start, end, err = p.Match(context.Background(), tc.source)
		if err != nil || yes != tc.want || yes && tc.source[start:end] != tc.piece {
			t.Fatal(tc, yes, start, end, err)
		}
	}
}
func TestSimilarCompilerDiagnostics(t *testing.T) {
	for _, tc := range []struct{ pattern, reason string }{
		{"[", "brackets [] not balanced"}, {"(", "parentheses () not balanced"}, {")", "parentheses () not balanced"},
		{"a{2,1}", "invalid repetition count(s)"}, {"a{256}", "invalid repetition count(s)"}, {"[z-a]", "invalid character range"}, {"[a-c-e]", "invalid character range"},
		{"[[:bogus:]]", "invalid character class"}, {"[[.ch.]]", "invalid collating element"},
		{`\q`, "invalid escape \\ sequence"}, {`\1`, "invalid backreference number"}, {`(ab)\1`, "invalid backreference number"},
		{"*a", "quantifier operand invalid"}, {"a**", "quantifier operand invalid"}, {"%*", "quantifier operand invalid"},
		{`\u123`, "invalid escape \\ sequence"}, {`\UFFFFFFFF`, "invalid escape \\ sequence"}, {`[\A]`, "invalid escape \\ sequence"},
		{`[\1]`, "invalid escape \\ sequence"}, {`[[=a=]-z]`, "invalid character range"},
	} {
		canonical, err := TranslateSimilar(context.Background(), tc.pattern, "\\")
		if err != nil {
			t.Fatal(err)
		}
		_, err = CompileSimilar(context.Background(), canonical, CollationRef{ID: 950})
		var state *sqlstate.Error
		if !errors.As(err, &state) || state.Code != "2201B" || state.Message != "invalid regular expression: "+tc.reason {
			t.Fatal(tc, err)
		}
	}
	canonical, err := TranslateSimilar(context.Background(), `#"a#1#"`, "#")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompileSimilar(context.Background(), canonical, CollationRef{ID: 950}); err == nil {
		t.Fatal("open capture backreference accepted")
	}
}
func TestSimilarBudgetsAndCancellation(t *testing.T) {
	canonical, err := TranslateSimilar(context.Background(), "a%", "\\")
	if err != nil {
		t.Fatal(err)
	}
	_, err = compileSimilarBudget(context.Background(), canonical, CollationRef{ID: 950}, 1)
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Message != "invalid regular expression: regular expression is too complex" {
		t.Fatal(err)
	}
	p := similarTestProgram(t, "%", "\\", 950)
	p.budget.limit = p.budget.used + 1
	_, _, _, err = p.Match(context.Background(), "a")
	if !errors.As(err, &state) || state.Message != "regular expression failed: regular expression is too complex" {
		t.Fatal(err)
	}
	p = similarTestProgram(t, `#"%#"`, "#", 950)
	p.budget.limit = p.budget.used + 1
	if _, _, _, err = p.Match(context.Background(), "a"); !errors.As(err, &state) || state.Message != "regular expression failed: regular expression is too complex" {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = CompileSimilar(canceled, canonical, CollationRef{ID: 950}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx := &likeCountingContext{Context: context.Background()}
	canonical = "^(?:" + strings.Repeat("a", 20000) + ")$"
	if _, err = CompileSimilar(ctx, canonical, CollationRef{ID: 950}); !errors.Is(err, context.Canceled) {
		t.Fatal(err, ctx.calls)
	}
	for _, pattern := range []string{"%", `#"%#"`, `#"a+#"#1`} {
		p = similarTestProgram(t, pattern, "#", 950)
		ctx = &likeCountingContext{Context: context.Background()}
		if _, _, _, err = p.Match(ctx, strings.Repeat("a", 20000)); !errors.Is(err, context.Canceled) {
			t.Fatal(pattern, err, ctx.calls)
		}
	}
	// Deep syntax is an iterative compile, not recursive parser/lowering work.
	pattern := strings.Repeat("(", 5000) + "a" + strings.Repeat(")", 5000)
	p = similarTestProgram(t, pattern, "\\", 950)
	yes, _, _, err := p.Match(context.Background(), "a")
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
}

func TestSimilarRegularScratchReuse(t *testing.T) {
	p := similarTestProgram(t, "(a|aa)*b", "\\", 950)
	source := strings.Repeat("a", 1000) + "b"
	yes, _, _, err := p.Match(context.Background(), source)
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	allocated := testing.AllocsPerRun(10, func() {
		yes, _, _, err := p.Match(context.Background(), source)
		if err != nil || !yes {
			t.Fatal(yes, err)
		}
	})
	if allocated != 0 {
		t.Fatalf("reused regular scratch allocates %v times per match", allocated)
	}
	if p.matchWork.ctx != nil || p.budget.work != nil {
		t.Fatal("program retained execution context")
	}
}

func TestSimilarNonparticipatingCapture(t *testing.T) {
	for _, tc := range []struct{ pattern, source string }{
		{`((a#"b#")|d)`, "d"},
		{`#"a)|b(#"`, "b"},
	} {
		p := similarTestProgram(t, tc.pattern, "#", 950)
		if !p.HasCapture() {
			t.Fatal("lost introduced capture", tc.pattern)
		}
		yes, start, end, err := p.Match(context.Background(), tc.source)
		if err != nil || !yes || start != -1 || end != -1 {
			t.Fatal(tc, yes, start, end, err)
		}
	}
	p := similarTestProgram(t, "d", "#", 950)
	if p.HasCapture() {
		t.Fatal("invented capture")
	}
	yes, start, end, err := p.Match(context.Background(), "d")
	if err != nil || !yes || start != -1 || end != -1 {
		t.Fatal(yes, start, end, err)
	}
}

func TestSimilarRepeatedCaptureReleasePreferences(t *testing.T) {
	for _, tc := range []struct {
		pattern, source, piece string
		absent                 bool
	}{
		{`((#"a#")|b)*`, "a", "a", false},
		{`((#"a#")|b)*`, "ab", "", true},
		{`((#"a#")|b)*`, "ba", "a", false},
		{`((#"a#")|b)*`, "b", "", true},
		{`((#"a#")|b)*`, "", "", true},
		{`((#"a%#"))+`, "aaa", "a", false},
		{`((#"a%#"))+?`, "aaa", "aaa", false},
		{`((#"a%#")){2}`, "aaa", "aa", false},
		{`((#"a%#")){2}?`, "aaa", "aa", false},
	} {
		p := similarTestProgram(t, tc.pattern, "#", 950)
		yes, start, end, err := p.Match(context.Background(), tc.source)
		if err != nil || !yes {
			t.Fatal(tc, yes, start, end, err)
		}
		if tc.absent {
			if start != -1 || end != -1 {
				t.Fatal(tc, start, end)
			}
		} else if start < 0 || end < start || tc.source[start:end] != tc.piece {
			t.Fatal(tc, start, end)
		}
	}
}

func TestSimilarEscapedClassSetNegation(t *testing.T) {
	for _, tc := range []struct{ pattern, positive, negative string }{
		{`\d`, "5", "a"}, {`\D`, "a", "5"}, {`\s`, " ", "a"},
		{`\S`, "a", " "}, {`\w`, "_", "-"}, {`\W`, "-", "_"},
	} {
		p := similarTestProgram(t, tc.pattern, "\\", 950)
		for _, i := range p.code {
			if i.op == similarClass && i.value != 0 {
				t.Fatal("escaped class incorrectly negates whole set", tc, i)
			}
		}
		for _, sample := range []struct {
			source string
			want   bool
		}{{tc.positive, true}, {tc.negative, false}} {
			yes, _, _, err := p.Match(context.Background(), sample.source)
			if err != nil || yes != sample.want {
				t.Fatal(tc, sample, yes, err)
			}
		}
	}
}

func TestSimilarNullableIterationPreferences(t *testing.T) {
	for _, tc := range []struct {
		pattern, source, piece string
		absent                 bool
	}{
		{`#"a?)*(#"`, "", "", false},
		{`#"a??)*(#"`, "", "", true},
		{`#"a?)+(#"`, "", "", false},
		{`#"a+)*(#"`, "aaa", "aaa", false},
		{`#"a+?)*(#"`, "aaa", "a", false},
		{`((#"a+#"#1))+`, "aaaa", "a", false},
	} {
		p := similarTestProgram(t, tc.pattern, "#", 950)
		yes, start, end, err := p.Match(context.Background(), tc.source)
		if err != nil || !yes {
			t.Fatal(tc, yes, start, end, err)
		}
		if tc.absent {
			if start != -1 || end != -1 {
				t.Fatal(tc, start, end)
			}
		} else if start < 0 || end < start || tc.source[start:end] != tc.piece {
			t.Fatal(tc, start, end)
		}
	}
}
