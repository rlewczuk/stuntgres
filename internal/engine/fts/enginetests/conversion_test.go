package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/fts"
	"testing"
)

func convertTestNormalizer(ctx context.Context, text string) ([]fts.LexicalPosition, error) {
	tokens, err := fts.ParseDocument(ctx, text)
	if err != nil {
		return nil, err
	}
	var positions []fts.LexicalPosition
	for _, token := range tokens {
		if token.ID == 12 {
			continue
		}
		word := strings.ToLower(token.Text)
		var words []fts.Lexeme
		if word != "a" && word != "the" {
			words = []fts.Lexeme{{Text: word}}
		}
		positions = append(positions, fts.LexicalPosition{Position: len(positions) + 1, Lexemes: words})
	}
	return positions, nil
}
func TestFTSConvertCleanup(t *testing.T) {
	ctx := t.Context()
	for _, tc := range [][2]string{
		{"(1 <-> a) <-> 2", "1 <2> 2"},
		{"1 <-> (a <-> 2)", "1 <2> 2"},
		{"(1 <-> a) <3> 2", "1 <4> 2"},
		{"foo <-> a <-> the <-> bar", "foo <3> bar"},
		{"foo <-> 'the bar'", "foo <-> bar"},
		{"'foo the' <-> bar", "foo <-> bar"},
		{"foo <-> 'bar the baz'", "foo <-> (bar <2> baz)"},
		{"foo <-> 'the a' <-> bar", "foo <2> bar"},
		{"!(a & !b) & c", "!!b & c"},
		{"(a <2> the) <3> (x <-> y)", "x <-> y"},
		{"(x <-> a) & y <-> z", "x & y <-> z"},
		{"w <-> (((a <-> x) <2> (y <3> a)) <-> z)", "w <2> ((x <2> y) <4> z)"},
		{"!(a <3> the)", ""},
	} {
		got, _, err := fts.ConvertQuery(ctx, fts.StrictConversion, tc[0], func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(ctx, s) })
		if err != nil {
			t.Fatal(tc, err)
		}
		want, err := fts.ParseQuery(ctx, tc[1])
		if err != nil {
			t.Fatal(err)
		}
		equal, err := got.Equal(ctx, want)
		if err != nil || !equal {
			t.Fatalf("arena association/distance %s = %s, want %s: %v", tc[0], got, want, err)
		}
	}
	longMorph := "'" + strings.Repeat("cat ", 599) + "cat'"
	normalized, parsed, err := fts.ConvertQuery(ctx, fts.StrictConversion, longMorph, func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(ctx, s) })
	if err != nil || !parsed || normalized.String() != strings.Repeat("'cat' <-> ", 599)+"'cat'" || normalized.NodeCount() != 1199 {
		t.Fatal("long normalized morph", normalized, parsed, err)
	}
	// Typed query input still admits only one bounded lexeme per operand.
	if raw, err := fts.ParseQuery(ctx, longMorph); raw != nil || err == nil {
		t.Fatal("raw operand limit removed", raw, err)
	}
	for _, length := range []int{2046, 2047} {
		raw, err := fts.ParseQuery(ctx, strings.Repeat("x", length))
		if length == 2046 {
			if err != nil || raw.NodeCount() != 1 {
				t.Fatal("raw operand boundary", raw, err)
			}
		} else {
			var state interface{ SQLState() string }
			if raw != nil || !errors.As(err, &state) || state.SQLState() != "54000" {
				t.Fatal("raw operand overflow", raw, err)
			}
		}
	}
	for _, size := range []struct{ words, bytes int }{{1, 2047}, {514, 2046}} {
		words := make([]fts.Lexeme, size.words)
		word := strings.Repeat("x", size.bytes)
		for i := range words {
			words[i].Text = word
		}
		q, _, err := fts.ConvertQuery(ctx, fts.StrictConversion, "token", func(string) ([]fts.LexicalPosition, error) {
			return []fts.LexicalPosition{{Position: 1, Lexemes: words}}, nil
		})
		var state interface{ SQLState() string }
		if q != nil || !errors.As(err, &state) || state.SQLState() != "54000" {
			t.Fatal("normalized representation overflow", size, q, err)
		}
	}
	for _, source := range []string{"'the a' &", "'the a' <->"} {
		if q, _, err := fts.ConvertQuery(ctx, fts.StrictConversion, source, func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(ctx, s) }); q != nil || err == nil {
			t.Fatal("stopped morph bypassed grammar", q, err)
		}
	}
	for _, tc := range [][2]string{
		{`cat OR "" dog`, "cat | dog"},
		{"cat OR,dog", "cat | dog"},
		{"cat OR|dog", "cat | dog"},
		{"cat OR,", "cat & or"},
		{"cat OR-", "cat & or"},
		{"cat OR)", "cat & or"},
		{"cat OR-dog", "cat & (or-dog <-> or <-> dog)"},
		{`cat OR""`, "cat"},
		{`cat OR ""`, "cat"},
		{`"" OR dog`, "dog"},
		{`""`, ""},
	} {
		got, parsed, err := fts.ConvertQuery(ctx, fts.WebConversion, tc[0], func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(ctx, s) })
		if err != nil || !parsed {
			t.Fatal("web parsed operand", tc, got, parsed, err)
		}
		want, err := fts.ParseQuery(ctx, tc[1])
		if err != nil {
			t.Fatal(err)
		}
		if same, err := got.Equal(ctx, want); err != nil || !same {
			t.Fatal("web operator association", tc, got, want, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if q, _, err := fts.ConvertQuery(canceled, fts.StrictConversion, "x", func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(canceled, s) }); q != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(q, err)
	}
	for _, kind := range []fts.ConversionKind{fts.StrictConversion, fts.PlainConversion, fts.PhraseConversion, fts.WebConversion} {
		for _, boundary := range []int{2, 5, 10} {
			interior := &conversionCancelContext{Context: ctx, remaining: boundary}
			input := strings.Repeat("word ", 1024)
			if kind == fts.StrictConversion {
				input = strings.Repeat("word & ", 1024) + "word"
			}
			q, _, err := fts.ConvertQuery(interior, kind, input, func(s string) ([]fts.LexicalPosition, error) { return convertTestNormalizer(interior, s) })
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("interior conversion publication", kind, boundary, q, err)
			}
		}
		failure := errors.New("captured dictionary failure")
		q, _, err := fts.ConvertQuery(ctx, kind, "word", func(string) ([]fts.LexicalPosition, error) { return nil, failure })
		if q != nil || !errors.Is(err, failure) {
			t.Fatal("normalization error suppressed", kind, q, err)
		}
	}
}
