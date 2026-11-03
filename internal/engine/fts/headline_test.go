package fts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// This leaf fixture supplies explicit simple-dictionary associations. Actual
// configured chains, filters and multiword mappings are composed-owner tests.
func headlineUnitDocument(t *testing.T, text string) *HeadlineDocument {
	t.Helper()
	tokens, err := ParseDocument(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	var builder HeadlineBuilder
	builder.SetTokens(tokens)
	position := 0
	for i, token := range tokens {
		if token.ID == 12 || token.ID == 13 || token.ID == 14 || token.ID == 23 {
			continue
		}
		position++
		if err := builder.Associate(t.Context(), i, i+1, LexicalPosition{Position: position, Lexemes: []Lexeme{{Text: strings.ToLower(token.Text)}}}); err != nil {
			t.Fatal(err)
		}
	}
	document, err := builder.Finish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return document
}
func headlineUnit(t *testing.T, text, query, options, want string) {
	t.Helper()
	document := headlineUnitDocument(t, text)
	q, err := ParseQuery(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseHeadlineOptions(t.Context(), options, headlineUnitInteger)
	if err != nil {
		t.Fatal(err)
	}
	got, err := document.Headline(t.Context(), q, o)
	if err != nil || got != want {
		t.Fatalf("Headline(%q,%q,%q)=%q,%v want %q", text, query, options, got, err, want)
	}
}
func TestFTSHeadlineSelection(t *testing.T) {
	for _, tc := range []struct{ text, query, options, want string }{
		{"1 2 3 1 3", "1 <-> 3", "MaxWords=2,MinWords=1", "<b>1</b> <b>3</b>"},
		{"1 2 3 1 3", "1 & 3", "MaxWords=4,MinWords=1", "<b>1</b> 2 <b>3</b>"},
		{"alpha beta gamma delta", "missing", "MaxWords=3,MinWords=2", "alpha beta"},
		{"alpha beta cat gamma delta epsilon", "cat", "MaxWords=5,MinWords=1,ShortWord=0,MaxFragments=1", "alpha beta <b>cat</b> gamma delta"},
		{"cat alpha beta gamma delta epsilon rat dog zeta", "cat | (rat <-> dog)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=2,FragmentDelimiter=|", "<b>cat</b> alpha|<b>rat</b> <b>dog</b>"},
		{"cat alpha beta gamma delta epsilon rat dog zeta", "cat | (rat <-> dog)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=1", "<b>rat</b> <b>dog</b>"},
		{"cat rat alpha beta cat gamma delta", "cat & !rat", "MaxWords=3,MinWords=1,ShortWord=0,MaxFragments=1", "<b>cat</b>"},
	} {
		headlineUnit(t, tc.text, tc.query, tc.options, tc.want)
	}
}
func TestFTSHeadlineRendering(t *testing.T) {
	headlineUnit(t, "<em>Cat</em> &amp; dog", "cat", "HighlightAll=true", "<em><b>Cat</b></em> &amp; dog")
	headlineUnit(t, "<em>cat</em> &amp; dog", "cat", "", " <b>cat</b>  &amp; dog")
	headlineUnit(t, "Cat, café!\ncat & \"cat\" < >", "cat", "HighlightAll=true,StartSel=<<,StopSel=>>", "<<Cat>>, café!\n<<cat>> & \"<<cat>>\" < >")
	headlineUnit(t, "cat rat cat bird rat", "cat <-> rat", "HighlightAll=true", "<b>cat</b> <b>rat</b> <b>cat</b> bird <b>rat</b>")
	headlineUnit(t, "cat cat", "cat | cat:*", "HighlightAll=true", "<b>cat</b> <b>cat</b>")
	for _, marker := range []string{"StartSel", "StopSel", "FragmentDelimiter"} {
		document := headlineUnitDocument(t, "Cat")
		q, _ := ParseQuery(t.Context(), "cat")
		o, _ := ParseHeadlineOptions(t.Context(), marker+"="+strings.Repeat("é", 16384), headlineUnitInteger)
		got, err := document.Headline(t.Context(), q, o)
		if got != "" || err == nil {
			t.Fatal("oversized byte marker published", marker, got, err)
		}
	}
}
func TestFTSHeadlineCancellation(t *testing.T) {
	document := headlineUnitDocument(t, "alpha cat beta rat gamma cat delta rat epsilon cat zeta rat")
	q, _ := ParseQuery(t.Context(), "cat | (rat <-> gamma)")
	for _, options := range []string{"HighlightAll=true", "MaxWords=3,MinWords=1,MaxFragments=4"} {
		o, _ := ParseHeadlineOptions(t.Context(), options, headlineUnitInteger)
		baseline := &countingContext{Context: t.Context(), after: int(^uint(0) >> 1)}
		if _, err := document.Headline(baseline, q, o); err != nil {
			t.Fatal(err)
		}
		for after := 1; after <= baseline.calls; after++ {
			ctx := &countingContext{Context: t.Context(), after: after}
			got, err := document.Headline(ctx, q, o)
			if !errors.Is(err, context.Canceled) || got != "" {
				t.Fatal("headline partial publication", after, got, err)
			}
		}
	}
	t.Run("renderer", func(t *testing.T) {
		words := []headlineWord{
			{text: "Cat", in: true, selected: true},
			{text: " ", in: true},
			{text: "rat", in: true},
			{text: " omitted"},
			{text: "Cat", in: true, selected: true},
		}
		before := append([]headlineWord(nil), words...)
		options := HeadlineOptions{StartSel: "<b>", StopSel: "</b>", FragmentDelimiter: "|"}
		const want = "<b>Cat</b> rat|<b>Cat</b>"
		baseline := &countingContext{Context: t.Context(), after: int(^uint(0) >> 1)}
		if got, err := renderHeadlineWords(baseline, words, options); err != nil || got != want {
			t.Fatal("renderer baseline", got, err)
		}
		// Every token boundary and the completion fence must reject partial
		// strings without mutating the selected input words.
		for after := 1; after <= baseline.calls; after++ {
			ctx := &countingContext{Context: t.Context(), after: after}
			if got, err := renderHeadlineWords(ctx, words, options); !errors.Is(err, context.Canceled) || got != "" {
				t.Fatal("renderer partial publication", after, got, err)
			}
			if !reflect.DeepEqual(words, before) {
				t.Fatal("renderer mutated selected words", after)
			}
		}
		if got, err := renderHeadlineWords(t.Context(), words, options); err != nil || got != want {
			t.Fatal("renderer recovery", got, err)
		}
	})
}
func TestFTSHeadlineImmutability(t *testing.T) {
	document := headlineUnitDocument(t, "Cat rat cat rat")
	q, _ := ParseQuery(t.Context(), "cat & rat")
	before := *document
	before.tokens = append([]DocumentToken(nil), document.tokens...)
	before.links = append([]headlineTokenAssociations(nil), document.links...)
	before.associations = append([]headlineAssociation(nil), document.associations...)
	queryNodes := append([]queryNode(nil), q.nodes...)
	o, _ := ParseHeadlineOptions(t.Context(), "HighlightAll=true", headlineUnitInteger)
	for range 3 {
		if _, err := document.Headline(t.Context(), q, o); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &countingContext{Context: t.Context(), after: 8}
	if _, err := document.Headline(ctx, q, o); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, *document) || !reflect.DeepEqual(queryNodes, q.nodes) {
		t.Fatal("headline mutated raw/query backing")
	}
}
