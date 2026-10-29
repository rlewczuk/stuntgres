package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

type thesaurusCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *thesaurusCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func thesaurusUnitCompile(t *testing.T, file string) *ThesaurusDictionary {
	t.Helper()
	ctx := context.Background()
	rules, err := ReadThesaurusRules(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil {
		t.Fatal(err)
	}
	d, err := CompileThesaurusDictionary(ctx, rules, sub)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestThesaurusCompiler(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		file, input string
		want        []expr.PhraseLexeme
	}{
		{"stuntgres_thes_ties", "Alpha", nil},
		{"stuntgres_thes_bad_delimiter", "alpha", []expr.PhraseLexeme{{Text: "beta"}, {Text: ":", AddPosition: true}, {Text: "gamma", AddPosition: true}}},
		{"stuntgres_thes_comments", "alpha", []expr.PhraseLexeme{{Text: "ok"}}},
	} {
		d := thesaurusUnitCompile(t, tc.file)
		words, _, err := d.NewMatcher().Step(ctx, tc.input, false)
		if err != nil || !reflect.DeepEqual(words, tc.want) {
			t.Fatal(tc.file, words, err)
		}
	}
	for _, tc := range []struct{ file, side, word string }{
		{"stuntgres_thes_sample_stop", "sample", "the"}, {"stuntgres_thes_substitute_stop", "substitute", "the"},
	} {
		rules, err := ReadThesaurusRules(ctx, tc.file)
		if err != nil {
			t.Fatal(err)
		}
		sub, err := CompileSimpleDictionary(ctx, true, "english")
		if err != nil {
			t.Fatal(err)
		}
		d, err := CompileThesaurusDictionary(ctx, rules, sub)
		var diagnostic *sqlstate.Error
		if d != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Context != "" {
			t.Fatal(d, err)
		}
		want := "thesaurus " + tc.side + " word \"" + tc.word + "\" is a stop word (rule 1)"
		if diagnostic.Message != want {
			t.Fatal(diagnostic)
		}
		hint := ""
		if tc.side == "sample" {
			hint = `Use "?" to represent a stop word within a sample phrase.`
		}
		if diagnostic.Hint != hint {
			t.Fatal(diagnostic)
		}
	}
	rules, err := ReadThesaurusRules(ctx, "thesaurus_sample")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil {
		t.Fatal(err)
	}
	firstRule, firstWord := &rules.rules[0], &rules.rules[0].sample[0]
	rejecting, err := CompileSimpleDictionary(ctx, false, "english")
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := CompileThesaurusDictionary(ctx, rules, rejecting); failed != nil || err == nil {
		t.Fatal("normalization failure published parsed rules as executable state", failed, err)
	}
	compiled, err := CompileThesaurusDictionary(ctx, rules, sub)
	if err != nil {
		t.Fatal(err)
	}
	value, err := compiled.Lexize(ctx, "one")
	if err != nil || !value.Recognized || len(value.Lexemes) != 1 {
		t.Fatal("parsed input could not be reused after rejected compilation", value, err)
	}
	word := value.Lexemes[0].Text
	if word != "1" || len(rules.rules) != 8 || &rules.rules[0] != firstRule || &rules.rules[0].sample[0] != firstWord || *firstWord != "one" {
		t.Fatal("normalization changed immutable parsed input/backing", word, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileThesaurusDictionary(canceled, rules, sub); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled compiler publication", d, err)
	}
	reading, stop := context.WithCancel(ctx)
	defer stop()
	gated := &thesaurusCancelContext{Context: reading, cancel: stop, remaining: 4}
	if parsed, err := ReadThesaurusRules(gated, "thesaurus_sample"); parsed != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight resource reader published", parsed, err)
	}
}

func TestThesaurusMatcher(t *testing.T) {
	ctx := context.Background()
	d := thesaurusUnitCompile(t, "thesaurus_sample")
	first, second := d.NewMatcher(), d.NewMatcher()
	step := func(m expr.PhraseMatcher, input string, want []expr.PhraseLexeme, more bool) {
		t.Helper()
		got, next, err := m.Step(ctx, input, false)
		if err != nil || next != more || !reflect.DeepEqual(got, want) {
			t.Fatal(input, got, next, err)
		}
	}
	step(first, "ONE", []expr.PhraseLexeme{{Text: "1"}}, true)
	step(second, "booking", nil, true)
	step(first, "two", []expr.PhraseLexeme{{Text: "12"}}, true)
	step(second, "the", nil, true)
	step(first, "three", []expr.PhraseLexeme{{Text: "123"}}, false)
	step(second, "tickets", []expr.PhraseLexeme{{Text: "order"}, {Text: "invitation", AddPosition: true}, {Text: "cards", AddPosition: true}}, false)
	first.Reset()
	step(first, "booking", nil, true)
	step(first, "unknown", nil, false)
	first.Reset()
	step(first, "one", []expr.PhraseLexeme{{Text: "1"}}, true)
	if got, more, err := first.Step(ctx, "", true); got != nil || more || err != nil {
		t.Fatal("end request", got, more, err)
	}
	first.Reset()
	step(first, "two", []expr.PhraseLexeme{{Text: "2"}}, false)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := second.Step(canceled, "tickets", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	second.Reset()
	step(second, "one", []expr.PhraseLexeme{{Text: "1"}}, true)
	ties := thesaurusUnitCompile(t, "stuntgres_thes_ties").NewMatcher()
	step(ties, "ALPHA", nil, true)
	step(ties, "beta", []expr.PhraseLexeme{{Text: "last"}}, false)
	reverse := thesaurusUnitCompile(t, "stuntgres_thes_ties_reverse").NewMatcher()
	step(reverse, "alpha", nil, true)
	step(reverse, "BETA", []expr.PhraseLexeme{{Text: "first"}}, false)
}
