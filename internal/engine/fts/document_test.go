package fts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func assertDocumentTokens(t *testing.T, text string, want []DocumentToken) {
	t.Helper()
	got, err := ParseDocument(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document %q\ngot  %#v\nwant %#v", text, got, want)
	}
	for _, token := range got {
		if token.ID < 1 || token.ID > 23 || token.Text == "" {
			t.Fatalf("invalid emitted token: %#v", token)
		}
	}
}

func TestFTSParserDocumentTokens(t *testing.T) {
	t.Run("released139", func(t *testing.T) {
		if len(documentReleaseTokens) != 139 {
			t.Fatal("incomplete released corpus")
		}
		assertDocumentTokens(t, documentReleaseCorpus, documentReleaseTokens)
	})
	tests := []struct {
		text string
		want []DocumentToken
	}{
		{"", nil},
		{"elephant", []DocumentToken{{1, "elephant"}}},
		{"mañana", []DocumentToken{{2, "mañana"}}},
		{"beta1", []DocumentToken{{3, "beta1"}}},
		{"foo@example.com", []DocumentToken{{4, "foo@example.com"}}},
		{"http://example.com/stuff/index.html", []DocumentToken{{14, "http://"}, {5, "example.com/stuff/index.html"}, {6, "example.com"}, {18, "/stuff/index.html"}}},
		{"-1.234e56", []DocumentToken{{7, "-1.234e56"}}},
		{"8.3.0", []DocumentToken{{8, "8.3.0"}}},
		{"foo-bar-beta1", []DocumentToken{{15, "foo-bar-beta1"}, {11, "foo"}, {12, "-"}, {11, "bar"}, {12, "-"}, {9, "beta1"}}},
		{"qwe-wer", []DocumentToken{{16, "qwe-wer"}, {11, "qwe"}, {12, "-"}, {11, "wer"}}},
		{"mañana-niño", []DocumentToken{{17, "mañana-niño"}, {10, "mañana"}, {12, "-"}, {10, "niño"}}},
		{"<fr>", []DocumentToken{{13, "<fr>"}}},
		{"/usr/local/foo.txt", []DocumentToken{{19, "/usr/local/foo.txt"}}},
		{"-1.234", []DocumentToken{{20, "-1.234"}}},
		{"-1234", []DocumentToken{{21, "-1234"}}},
		{"1234", []DocumentToken{{22, "1234"}}},
		{"&amp;", []DocumentToken{{23, "&amp;"}}},
		{"123 - a number", []DocumentToken{{22, "123"}, {12, " "}, {12, "- "}, {1, "a"}, {12, " "}, {1, "number"}}},
	}
	seen := make(map[int32]bool)
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) { assertDocumentTokens(t, tt.text, tt.want) })
		for _, token := range tt.want {
			seen[token.ID] = true
		}
	}
	if len(seen) != 23 {
		t.Fatalf("category witnesses cover %d of 23 categories", len(seen))
	}
}

func TestFTSParserDocumentClasses(t *testing.T) {
	for _, text := range []string{"a🙂b", "a—b", "a\u00a0b", "a\u0301b", "\u0301a", "a\u0903b", "a\u200bb", "a\u3000b", "a９b"} {
		assertDocumentTokens(t, text, []DocumentToken{{2, text}})
	}
	assertDocumentTokens(t, "a b", []DocumentToken{{1, "a"}, {12, " "}, {1, "b"}})
	for _, text := range []string{strings.Repeat("A", 8192), strings.Repeat("🙂", 4096)} {
		id := int32(1)
		if text[0] >= 128 { id = 2 }
		assertDocumentTokens(t, text, []DocumentToken{{id, text}})
	}
	// Test every ASCII byte against the independent RFC/release whitelist;
	// predicates remain package-private, with no SQL descriptors in this leaf.
	for c := range 128 {
		p := documentParser{text: string(rune(c)), position: documentPosition{width: 1}}
		want := c >= 33 && c <= 126
		switch c { case '"', '<', '>', '\\', '^', '`', '{', '|', '}': want = false }
		if got := p.matches(documentAction{predicate: p_isurlchar}); got != want {
			t.Fatalf("URL predicate byte %d: got %v want %v", c, got, want)
		}
		alpha := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		if got := p.matches(documentAction{predicate: p_isalpha}); got != alpha {
			t.Fatalf("C alpha byte %d: %v", c, got)
		}
	}
	for _, c := range []rune{'🙂', 'é', '\u0301', '\u0903', '\u200b', '\u00a0', '９'} {
		p := documentParser{text: string(c), position: documentPosition{width: utf8.RuneLen(c)}}
		for _, predicate := range []documentPredicate{p_isalpha, p_isalnum} {
			if !p.matches(documentAction{predicate: predicate}) { t.Fatalf("%U is not alpha/alnum", c) }
		}
		for _, predicate := range []documentPredicate{p_isasclet, p_isdigit, p_isxdigit, p_isspace, p_isurlchar} {
			if p.matches(documentAction{predicate: predicate}) { t.Fatalf("%U matched ASCII class %d", c, predicate) }
		}
	}
}

func TestFTSParserDocumentTransitions(t *testing.T) {
	tests := []struct { text string; want []DocumentToken }{
		{"qwe@efd.r", []DocumentToken{{1, "qwe"}, {12, "@"}, {19, "efd.r"}}},
		{"readline-4.2", []DocumentToken{{1, "readline"}, {20, "-4.2"}}},
		{"gist.c.", []DocumentToken{{19, "gist.c"}, {12, "."}}},
		{"<i <b>", []DocumentToken{{12, "<"}, {1, "i"}, {12, " "}, {13, "<b>"}}},
		{"<a href=\"qwe<qwe>\">", []DocumentToken{{13, "<a href=\"qwe<qwe>\">"}}},
		{`<a x='a\'b'>`, []DocumentToken{{13, `<a x='a\'b'>`}}},
		{"<!-- a > b -->", []DocumentToken{{13, "<!-- a > b -->"}}},
		{"<?xml version='1.0'?>", []DocumentToken{{13, "<?xml version='1.0'?>"}}},
		{"<!DOCTYPE html>", []DocumentToken{{13, "<!DOCTYPE html>"}}},
		{"<script>Word</script>", []DocumentToken{{13, "<script>"}, {12, "Word"}, {13, "</script>"}}},
		{"<STYLE>Word</STYLE>", []DocumentToken{{13, "<STYLE>"}, {12, "Word"}, {13, "</STYLE>"}}},
		{"http://www.com/ ", []DocumentToken{{14, "http://"}, {6, "www.com"}, {12, "/ "}}},
		{"7aew.werc.ewr:8100/?ad=qwe&dw=%20%32", []DocumentToken{{5, "7aew.werc.ewr:8100/?ad=qwe&dw=%20%32"}, {6, "7aew.werc.ewr:8100"}, {18, "/?ad=qwe&dw=%20%32"}}},
		{"-1.2.3", []DocumentToken{{12, "-"}, {8, "1.2.3"}}},
		{"1e+", []DocumentToken{{3, "1e"}, {12, "+"}}},
		{"&#12; &#xAf;", []DocumentToken{{23, "&#12;"}, {12, " "}, {23, "&#xAf;"}}},
		{"http://example.com/x🙂", []DocumentToken{{14, "http://"}, {5, "example.com/x"}, {6, "example.com"}, {18, "/x"}, {2, "🙂"}}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) { assertDocumentTokens(t, tt.text, tt.want) })
	}
}

// A deterministic context cancels at a selected cancellation boundary, without
// sleeps, workers or a production instrumentation hook.
type documentCancelContext struct {
	context.Context
	remaining int
}

func (c *documentCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 { return context.Canceled }
	return nil
}

func TestFTSParserDocumentCancellation(t *testing.T) {
	tests := []struct { name, text string; boundary int }{
		{"before", "Word", 1},
		{"singleToken", strings.Repeat("a", 8192), 2},
		{"hostLookahead", "a@"+strings.Repeat("b", 8192)+".com", 2},
		{"pathLookahead", "example.com/"+strings.Repeat("a", 8192), 2},
		// The whole hyphenated word is recognized after 4099 steps; the
		// eighteenth Err call is inside component replay, not initial scan.
		{"hyphenReplay", strings.Repeat("a", 4096)+"-b", 18},
		{"tentativeStack", "a"+strings.Repeat("-a", 4096)+".", 2},
		{"completedPrefix", "Word "+strings.Repeat("a", 8192), 2},
		{"finalization", "Word", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &documentCancelContext{Context: context.Background(), remaining: tt.boundary}
			before := strings.Clone(tt.text)
			got, err := ParseDocument(ctx, tt.text)
			if !errors.Is(err, context.Canceled) || got != nil {
				t.Fatalf("cancellation published %#v, error %v", got, err)
			}
			if tt.text != before { t.Fatal("input mutated") }
			assertDocumentTokens(t, "Word", []DocumentToken{{1, "Word"}})
		})
	}
}
