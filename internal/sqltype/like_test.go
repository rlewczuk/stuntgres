package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLikeProviderLazyEscapes(t *testing.T) {
	var text, boolean *Descriptor
	for _, d := range Builtins() {
		if d.ID() == Text {
			text = d
		}
		if d.ID() == Bool {
			boolean = d
		}
	}
	for _, tc := range []struct {
		input, pattern string
		want, fails    bool
	}{
		{"abc", "a%", true, false}, {"abc", "b", false, false},
		{"é", "_", true, false}, {"é", "_", false, false},
		{"", "\\", false, false}, {"x", "\\", false, true},
		{"x", "z\\", false, false}, {"x", "x\\", false, false},
		{"xx", "x\\", false, true}, {"x", "%\\", false, true},
		{"", "%\\", false, false},
		{"x", "%_\\", false, true}, {"x", "%__\\", false, false},
		{"", "%_\\", false, false}, {"", "%__\\", false, false},
		{"xx", "%x\\", false, true}, {"yx", "%x\\", false, false},
		{"xx", "%_%x\\", false, false}, {"xxx", "%_%x\\", false, true},
	} {
		p, err := BindLike("~~", text.Usage(), text.Usage(), boolean.Usage(), false)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(context.Background(), String(text, tc.input), String(text, tc.pattern))
		if tc.fails {
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != "22025" {
				t.Fatal(tc, err)
			}
		} else if err != nil || v != Boolean(boolean, tc.want) {
			t.Fatal(tc, v, err)
		}
	}
}

func TestLikeProviderEscapeBeforeFolding(t *testing.T) {
	var text, boolean *Descriptor
	for _, d := range Builtins() {
		if d.ID() == Text {
			text = d
		}
		if d.ID() == Bool {
			boolean = d
		}
	}
	escape, err := BindLikeEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	p, err := BindLike("~~*", text.Usage(), text.Usage(), boolean.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input, pattern, escape string
		want                   bool
	}{
		{"ab", "a_", "A", true}, {"xa", "X_", "x", true},
		{"a%b", "aé%b", "é", true}, {"É", "é", "", false},
		{"a\\b", "a\\b", "", true},
	} {
		normalized, err := escape.Evaluate(context.Background(), String(text, tc.pattern), String(text, tc.escape))
		if err != nil {
			t.Fatal(tc, err)
		}
		v, err := p.Evaluate(context.Background(), String(text, tc.input), normalized)
		if err != nil || v != Boolean(boolean, tc.want) {
			t.Fatal(tc, v, err)
		}
	}
}

type likeCountingContext struct {
	context.Context
	calls int
}

func (c *likeCountingContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func TestLikeProviderCancellationAndBinary(t *testing.T) {
	var text, binary, boolean *Descriptor
	for _, d := range Builtins() {
		switch d.ID() {
		case Text:
			text = d
		case Bytea:
			binary = d
		case Bool:
			boolean = d
		}
	}
	p, err := BindLike("~~", text.Usage(), text.Usage(), boolean.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &likeCountingContext{Context: context.Background()}
	if _, err := p.Evaluate(ctx, String(text, strings.Repeat("a", 20000)), String(text, "%"+strings.Repeat("a", 10000)+"b")); !errors.Is(err, context.Canceled) {
		t.Fatal(err, ctx.calls)
	}
	escape, err := BindLikeEscape(text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	ctx = &likeCountingContext{Context: context.Background()}
	if _, err := escape.Evaluate(ctx, String(text, strings.Repeat("é", 20000)), String(text, "!")); !errors.Is(err, context.Canceled) {
		t.Fatal(err, ctx.calls)
	}
	bp, err := BindLike("~~", binary.Usage(), binary.Usage(), boolean.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input, pattern string
		want           bool
	}{
		{"\xc3\xa9", "_", false}, {"\xc3\xa9", "__", true}, {"\x00\xff", "__", true}, {"\x00\xff", "%", true}, {"\x00\xff", "\x00\xff", true},
	} {
		a, _ := Binary(binary, tc.input)
		b, _ := Binary(binary, tc.pattern)
		got, err := bp.Evaluate(context.Background(), a, b)
		if err != nil || got != Boolean(boolean, tc.want) {
			t.Fatal(tc, got, err)
		}
	}
}
