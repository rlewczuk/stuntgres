package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSimilarTranslateCanonical(t *testing.T) {
	for _, tc := range []struct{ pattern, escape, want string }{
		{"abc", "\\", "^(?:abc)$"},
		{"%_(a|b).^$", "\\", "^(?:.*.(?:a|b)\\.\\^\\$)$"},
		{`a#"b#"c`, "#", `^(?:a){1,1}?(b){1,1}(?:c)$`},
		{`a#"b`, "#", `^(?:a){1,1}?(b)$`},
		{`(a#"b#")+`, "#", `^(?:(?:a){1,1}?(b){1,1}(?:)+)$`},
		{`[#"]`, "#", `^(?:[\"])$`},
		{`[]#"]`, "#", `^(?:[]\"])$`},
		{`[[:alpha:]]%`, "\\", `^(?:[[:alpha:]].*)$`},
		{`[\]`, "", `^(?:[\\])$`},
		{`a\b`, "", `^(?:a\\b)$`},
		{`aé%b`, "é", `^(?:a\%b)$`},
		{`é界`, "é", `^(?:\界)$`},
		{"a#", "#", "^(?:a)$"},
		{"aé", "é", "^(?:a)$"},
	} {
		got, err := TranslateSimilar(context.Background(), tc.pattern, tc.escape)
		if err != nil || got != tc.want {
			t.Fatalf("pattern %q escape %q: got %q err %v, want %q", tc.pattern, tc.escape, got, err, tc.want)
		}
	}
}
func TestSimilarTranslateErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct{ pattern, escape, code, message, hint string }{
		{"x", "ab", "22025", "invalid escape string", "Escape string must be empty or one character."},
		{`#"#"#"`, "#", "2200C", "SQL regular expression may not contain more than two escape-double-quote separators", ""},
	} {
		_, err := TranslateSimilar(context.Background(), tc.pattern, tc.escape)
		var state *sqlstate.Error
		if !errors.As(err, &state) || state.Code != tc.code || state.Message != tc.message || state.Hint != tc.hint {
			t.Fatal(tc, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TranslateSimilar(canceled, "a", "\\"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx := &likeCountingContext{Context: context.Background()}
	if _, err := TranslateSimilar(ctx, strings.Repeat("é", 20000), "#"); !errors.Is(err, context.Canceled) {
		t.Fatal(err, ctx.calls)
	}
}
