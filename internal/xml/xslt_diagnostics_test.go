package xml

import (
	"context"
	"errors"
	"testing"
)

func TestXSLTDiagnostics(t *testing.T) {
	good := xsltTestStylesheet("", "OK")
	xsltExpectError(t, "", good, "", XSLTSourceParse, "")
	xsltExpectError(t, "<r>", "<bad/>", "p=[", XSLTSourceParse, "line 1: Premature end of data in tag r line 1\n<r>\n   ^")
	xsltExpectError(t, "\xef\xbb\xbf", good, "", XSLTSourceParse, "line 1: Start tag expected, '<' not found\n\ufeff\n^")
	xsltExpectError(t, "<r/>", "\xef\xbb\xbf", "", XSLTStyleParse, "line 1: Start tag expected, '<' not found\n\ufeff\n^")
	xsltExpectError(t, "<r/>", "", "", XSLTStyleParse, "")
	xsltExpectError(t, "<r/>", "<bad/>", "p=[", XSLTCompile, "")
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:value-of select="$missing"/>`), "", XSLTApply, "Undefined variable")
	// Source-derived ERROR-versus-STOPPED witnesses: a generic transform error
	// cannot turn a later real XPath callback into an empty diagnostic.
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:comment>--</xsl:comment><xsl:value-of select="$missing"/>`), "", XSLTApply, "Undefined variable")
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:copy-of select="document('file:///nonexistent-stuntgres-xslt')"/><xsl:value-of select="$missing"/>`), "", XSLTApply, "Undefined variable")
	prefix := "input conversion failed due to input error, bytes 0xC3 0xA9 0x41 0x42\ninput conversion failed due to input error, bytes 0xC3 0xA9 0x41 0x42\nencoder error"
	for _, codec := range []string{"ISO-2022-KR", "ISO-2022-CN"} {
		input := `<?xml version="1.0" encoding="` + codec + `"?><r/>éABCD`
		xsltExpectError(t, input, "<bad/>", "", XSLTCompile, prefix)
		xsltExpectText(t, input, good, "", "OK")
	}
	// Same physical bytes: strict callers reject the whole conversion, while only
	// the XSLT pointer outcome finalizes the genuinely completed collected tree.
	for _, tc := range []struct {
		codec, tail string
		events      int
	}{{"ASCII", "é", 0}, {"ISO-2022-KR", "éABCD", 3}, {"ISO-2022-CN", "éABCD", 3}, {"UTF-7", "+3AA-ABCD", 3}} {
		input := `<?xml version="1.0" encoding="` + tc.codec + `"?><r/>` + tc.tail
		policy := parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, legacyDiagnostics: true}
		strict, tree, events, err := parseConfigured(context.Background(), input, Options{Mode: Document, InputKind: RawMemory}, policy)
		var raw *RawInputError
		if strict.Valid() || tree != nil || !errors.As(err, &raw) || len(events) != tc.events {
			t.Fatalf("strict %s result=%+v tree=%p events=%+v error=%v", tc.codec, strict, tree, events, err)
		}
		policy.xsltReader = true
		accepted, tree, events, err := parseConfigured(context.Background(), input, Options{Mode: Document, InputKind: RawMemory}, policy)
		if err != nil || accepted.Valid() || tree == nil || len(tree.nodes) == 0 || len(events) != tc.events {
			t.Fatalf("XSLT %s result=%+v tree=%p events=%+v error=%v", tc.codec, accepted, tree, events, err)
		}
		frame, err := newXPathTree(context.Background(), tree)
		if err != nil {
			t.Fatal(err)
		}
		if len(frame.tree.root.children) != 1 || frame.tree.root.children[0].name != "r" {
			t.Fatal("not the actual completed source tree")
		}
		open := `<?xml version="1.0" encoding="` + tc.codec + `"?><r>` + tc.tail
		_, tree, _, err = parseConfigured(context.Background(), open, Options{Mode: Document, InputKind: RawMemory}, policy)
		if err == nil || tree != nil {
			t.Fatalf("open-root conversion published %s tree=%p error=%v", tc.codec, tree, err)
		}
	}
	xsltExpectError(t, `<?xml version="1.0" encoding="UTF-7"?><r/>+AOk-`, good, "", XSLTSourceParse, "line 1: Extra content at the end of the document\n?><r/>é\n      ^")
}
