package xml

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func xsltOutputText(value string) *xsltResultNode {
	return &xsltResultNode{Kind: xsltResultText, Value: value}
}
func xsltOutputElement(name string, children ...*xsltResultNode) *xsltResultNode {
	return &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Local: name}, Children: children}
}
func xsltOutputMethod(method string) xsltOutputProperties {
	return xsltOutputProperties{Method: xsltName{Local: method}, MethodSet: true}
}
func xsltOutputAssert(t *testing.T, tree *xsltResultTree, props xsltOutputProperties, want string) XSLTResult {
	t.Helper()
	got, err := saveXSLT(context.Background(), tree, props)
	if err != nil || got.Null || got.Text != want {
		t.Fatalf("save: bytes %x (%q), null %t, error %v; want %x (%q)", got.Text, got.Text, got.Null, err, want, want)
	}
	return got
}

// These private expectations are intrinsic serializer/encoding outcomes, not
// synthetic oracle evidence. Public dual-mode qualification is separate.
func TestXSLTOutput(t *testing.T) {
	t.Run("XML", func(t *testing.T) {
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("out", xsltOutputText("<&>\r\né😀"))}}
		xsltOutputAssert(t, tree, xsltOutputProperties{}, "<?xml version=\"1.0\"?>\n<out>&lt;&amp;&gt;&#13;\n&#xE9;&#x1F600;</out>\n")
		props := xsltOutputProperties{OmitDeclaration: true, OmitDeclarationSet: true, Encoding: "UTF-8", EncodingSet: true}
		xsltOutputAssert(t, tree, props, "<out>&lt;&amp;&gt;&#13;\né😀</out>\n")
		props.IndentSet = true
		xsltOutputAssert(t, tree, props, "<out>&lt;&amp;&gt;&#13;\né😀</out>")
		props.OmitDeclaration = false
		props.Version = "1.1"
		props.VersionSet = true
		props.Standalone = "yes"
		props.StandaloneSet = true
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("e")}}, props, "<?xml version=\"1.1\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<e/>")
		props.Standalone = "no"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("e")}}, props, "<?xml version=\"1.1\" encoding=\"UTF-8\" standalone=\"no\"?>\n<e/>")
	})
	t.Run("TextAndEmpty", func(t *testing.T) {
		props := xsltOutputMethod("text")
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("T<&"), {Kind: xsltResultComment, Value: "not text"}, {Kind: xsltResultPI, Name: xsltName{Local: "p"}, Value: "not text"}, xsltOutputElement("a", xsltOutputText("A"), xsltOutputElement("b", xsltOutputText("B")))}}
		xsltOutputAssert(t, tree, props, "T<&AB")
		for _, encoding := range []string{"UTF-8", "UTF-16", "UTF-16LE", "UTF-16BE", "UTF-32", "ISO-2022-KR", "UTF-7"} {
			props.Encoding = encoding
			props.EncodingSet = true
			xsltOutputAssert(t, &xsltResultTree{}, props, "")
			xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("")}}, props, "")
		}
	})
	t.Run("NamespacesAndAttributes", func(t *testing.T) {
		inner := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Local: "e"}, Namespaces: []xsltNamespace{{URI: ""}}}
		outer := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Local: "r", URI: "urn:r"}, Namespaces: []xsltNamespace{{URI: "urn:r"}, {Prefix: "p", URI: "urn:p"}}, Attributes: []xsltResultAttribute{{Name: xsltName{Prefix: "p", Local: "a", URI: "urn:p"}, Value: "\"\t\n\r<&é"}}, Children: []*xsltResultNode{inner}}
		inner.Children = []*xsltResultNode{{Kind: xsltResultElement, Name: xsltName{Prefix: "p", Local: "n", URI: "urn:p"}, Namespaces: []xsltNamespace{{Prefix: "p", URI: "urn:p"}}}}
		props := xsltOutputProperties{OmitDeclaration: true}
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{outer}}, props, "<r xmlns=\"urn:r\" xmlns:p=\"urn:p\" p:a=\"&quot;&#9;&#10;&#13;&lt;&amp;&#xE9;\"><e xmlns=\"\"><p:n/></e></r>\n")
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{inner}}, props, "<e><p:n xmlns:p=\"urn:p\"/></e>\n")
	})
	t.Run("NamespaceQuotation", func(t *testing.T) {
		// Report16 independently observed all four default/explicit-UTF8
		// namespace datums. Unlike ordinary attributes, xmlNsDumpOutput's
		// quoted-string writer preserves raw ampersands and nonASCII bytes.
		for _, tc := range []struct{ uri, encoding, want string }{
			{"urn:n?a=1&b=2", "", "<?xml version=\"1.0\"?>\n<p:e xmlns:p=\"urn:n?a=1&b=2\"/>\n"},
			{"urn:n?a=1&b=2", "UTF-8", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<p:e xmlns:p=\"urn:n?a=1&b=2\"/>\n"},
			{"urn:é", "", "<?xml version=\"1.0\"?>\n<p:e xmlns:p=\"urn:é\"/>\n"},
			{"urn:é", "UTF-8", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<p:e xmlns:p=\"urn:é\"/>\n"},
		} {
			t.Run(tc.uri+tc.encoding, func(t *testing.T) {
				root := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Prefix: "p", Local: "e", URI: tc.uri}, Namespaces: []xsltNamespace{{Prefix: "p", URI: tc.uri}}}
				props := xsltOutputProperties{Encoding: tc.encoding, EncodingSet: tc.encoding != ""}
				xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{root}}, props, tc.want)
			})
		}
		// Delimiter selection is literal quotation, not an attribute-escape
		// exception limited to ampersands. Both quotes require only &quot;.
		for _, tc := range []struct{ uri, quoted string }{
			{`urn:a"b&é`, `'urn:a"b&é'`},
			{`urn:a"b'c&é`, `"urn:a&quot;b'c&é"`},
			{"urn:<&>\t\r\né", "\"urn:<&>\t\r\né\""},
			{strings.Repeat("u", 4095) + `"&é'`, `"` + strings.Repeat("u", 4095) + `&quot;&é'"`},
		} {
			root := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Local: "e", URI: tc.uri}, Namespaces: []xsltNamespace{{URI: tc.uri}}}
			xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{root}}, xsltOutputProperties{OmitDeclaration: true}, "<e xmlns="+tc.quoted+"/>\n")
		}
		// Namespace output still uses the genuine selected encoder; ordinary
		// attribute/text escaping remains unchanged beside that same URI.
		root := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Prefix: "p", Local: "e", URI: "urn:é&"}, Namespaces: []xsltNamespace{{Prefix: "p", URI: "urn:é&"}}, Attributes: []xsltResultAttribute{{Name: xsltName{Local: "a"}, Value: "é&"}}, Children: []*xsltResultNode{xsltOutputText("é&")}}
		tree := &xsltResultTree{Children: []*xsltResultNode{root}}
		props := xsltOutputProperties{OmitDeclaration: true}
		xsltOutputAssert(t, tree, props, "<p:e xmlns:p=\"urn:é&\" a=\"&#xE9;&amp;\">&#xE9;&amp;</p:e>\n")
		props.Encoding, props.EncodingSet = "ISO-8859-1", true
		xsltOutputAssert(t, tree, props, "<p:e xmlns:p=\"urn:\xe9&\" a=\"\xe9&amp;\">\xe9&amp;</p:e>\n")
		props.Encoding = "ASCII"
		xsltOutputAssert(t, tree, props, "<p:e xmlns:p=\"urn:&#233;&\" a=\"&#233;&amp;\">&#233;&amp;</p:e>\n")
	})
	t.Run("IndentMixedAndMisc", func(t *testing.T) {
		props := xsltOutputProperties{OmitDeclaration: true, Indent: true, IndentSet: true}
		tree := &xsltResultTree{Children: []*xsltResultNode{{Kind: xsltResultComment, Value: "C"}, xsltOutputElement("r", xsltOutputElement("a"), &xsltResultNode{Kind: xsltResultPI, Name: xsltName{Local: "p"}, Value: "D"}, xsltOutputElement("b", xsltOutputText("X")))}}
		xsltOutputAssert(t, tree, props, "<!--C-->\n<r>\n  <a/>\n  <?p D?>\n  <b>X</b>\n</r>\n")
		tree.Children = []*xsltResultNode{xsltOutputElement("r", xsltOutputText("A"), xsltOutputElement("a", xsltOutputElement("b")), xsltOutputText("B"))}
		xsltOutputAssert(t, tree, props, "<r>A<a><b/></a>B</r>\n")
		tree.Children = []*xsltResultNode{xsltOutputElement("a"), xsltOutputText("T"), xsltOutputElement("b")}
		xsltOutputAssert(t, tree, props, "<a/>T<b/>\n")
	})
	t.Run("CDATAAndDOE", func(t *testing.T) {
		props := xsltOutputProperties{OmitDeclaration: true, CDATA: map[string]bool{"\x00r": true}}
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("r", xsltOutputText("A]]>B<&"), &xsltResultNode{Kind: xsltResultText, Value: "<raw/>", DisableEscaping: true})}}
		xsltOutputAssert(t, tree, props, "<r><![CDATA[A]]]]><![CDATA[>B<&<raw/>]]></r>\n")
		tree.Children[0].Name = xsltName{Prefix: "p", Local: "r", URI: "urn:p"}
		tree.Children[0].Namespaces = []xsltNamespace{{Prefix: "p", URI: "urn:p"}}
		xsltOutputAssert(t, tree, props, "<p:r xmlns:p=\"urn:p\">A]]&gt;B&lt;&amp;<raw/></p:r>\n")
		props.CDATA = map[string]bool{"urn:p\x00r": true}
		xsltOutputAssert(t, tree, props, "<p:r xmlns:p=\"urn:p\"><![CDATA[A]]]]><![CDATA[>B<&<raw/>]]></p:r>\n")
		text := strings.Repeat("A", 4095) + "]]>é"
		tree.Children = []*xsltResultNode{xsltOutputElement("r", xsltOutputText(text))}
		props.CDATA = map[string]bool{"\x00r": true}
		xsltOutputAssert(t, tree, props, "<r><![CDATA["+strings.Repeat("A", 4095)+"]]]]><![CDATA[>é]]></r>\n")
		tree.Children = []*xsltResultNode{xsltOutputElement("r", xsltOutputText("A]"), xsltOutputText("]"), xsltOutputText(">B"), xsltOutputText("C"))}
		xsltOutputAssert(t, tree, props, "<r><![CDATA[A]]]]><![CDATA[>BC]]></r>\n")
	})
	t.Run("CDATAOutputMethodPrecedence", func(t *testing.T) {
		// Released xsltCopyTextString selects CDATA only for XML output,
		// before considering DOE. HTML retains the actual text instead.
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("html", xsltOutputElement("p", xsltOutputText("A")))}}
		props := xsltOutputProperties{CDATA: map[string]bool{"\x00p": true}}
		xsltOutputAssert(t, tree, props, "<html><p>A</p></html>\n")
		props.Method, props.MethodSet = xsltName{Local: "html"}, true
		xsltOutputAssert(t, tree, props, "<html><p>A</p></html>\n")
		props.Method.Local = "xml"
		props.OmitDeclaration = true
		xsltOutputAssert(t, tree, props, "<html><p><![CDATA[A]]></p></html>\n")
		doe := &xsltResultNode{Kind: xsltResultText, Value: "<a/>", DisableEscaping: true}
		tree.Children = []*xsltResultNode{xsltOutputElement("p", doe)}
		xsltOutputAssert(t, tree, props, "<p><![CDATA[<a/>]]></p>\n")
		props.CDATA = nil
		xsltOutputAssert(t, tree, props, "<p><a/></p>\n")
		props.Method.Local = "html"
		props.CDATA = map[string]bool{"\x00p": true}
		doe.DisableEscaping = false
		xsltOutputAssert(t, tree, props, "<p>&lt;a/&gt;</p>\n")
		doe.DisableEscaping = true
		xsltOutputAssert(t, tree, props, "<p><a/></p>\n")
		props.Method.Local = "text"
		xsltOutputAssert(t, tree, props, "<a/>")
	})
	t.Run("Doctype", func(t *testing.T) {
		tree := &xsltResultTree{Children: []*xsltResultNode{{Kind: xsltResultComment, Value: "C"}, xsltOutputElement("r")}}
		props := xsltOutputProperties{DoctypePublic: "pub", DoctypePublicSet: true, DoctypeSystem: "sys", DoctypeSystemSet: true}
		xsltOutputAssert(t, tree, props, "<?xml version=\"1.0\"?>\n<!DOCTYPE r PUBLIC \"pub\" \"sys\">\n<!--C-->\n<r/>\n")
		props.DoctypePublic = ""
		props.DoctypePublicSet = false
		props.IndentSet = true
		props.OmitDeclaration = true
		xsltOutputAssert(t, tree, props, "<!DOCTYPE r SYSTEM \"sys\"><!--C--><r/>")
		props.DoctypePublicSet = true
		props.DoctypeSystem = ""
		xsltOutputAssert(t, tree, props, "<!DOCTYPE r PUBLIC \"\" \"\"><!--C--><r/>")
		props.DoctypePublic = "a\"b'c"
		props.DoctypeSystemSet = false
		xsltOutputAssert(t, tree, props, "<!DOCTYPE r PUBLIC \"a&quot;b'c\" \"\"><!--C--><r/>")
		props = xsltOutputMethod("html")
		props.Version = "5"
		props.VersionSet = true
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("html")}}, props, "<!DOCTYPE html SYSTEM \"about:legacy-compat\">\n<html></html>\n")
	})
	t.Run("HTML", func(t *testing.T) {
		input := xsltOutputElement("input")
		input.Attributes = []xsltResultAttribute{{Name: xsltName{Local: "checked"}, Value: "anything"}}
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("html", xsltOutputElement("br"), input)}}
		xsltOutputAssert(t, tree, xsltOutputMethod("html"), "<html>\n<br><input checked>\n</html>\n")
		xsltOutputAssert(t, tree, xsltOutputProperties{}, "<html>\n<br><input checked>\n</html>\n")
		props := xsltOutputMethod("html")
		props.IndentSet = true
		xsltOutputAssert(t, tree, props, "<html><br><input checked></html>\n")
		tree.Children = []*xsltResultNode{xsltOutputElement("div", xsltOutputElement("script", xsltOutputText("if (a < b && é) {}")), xsltOutputElement("style", xsltOutputText("a>b{}")), xsltOutputElement("p", xsltOutputText("é<&{x}")))}
		xsltOutputAssert(t, tree, props, "<div><script>if (a < b && é) {}</script><style>a>b{}</style><p>é&lt;&amp;{x}</p></div>\n")
		foreign := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Prefix: "p", Local: "br", URI: "urn:p"}, Namespaces: []xsltNamespace{{Prefix: "p", URI: "urn:p"}}}
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{foreign}}, props, "<p:br xmlns:p=\"urn:p\"></p:br>\n")
		tree.Children = []*xsltResultNode{xsltOutputText("X"), xsltOutputElement("html")}
		xsltOutputAssert(t, tree, xsltOutputProperties{}, "<?xml version=\"1.0\"?>\nX<html/>\n")
	})
	t.Run("HTMLMetaURIAndQuotes", func(t *testing.T) {
		anchor := xsltOutputElement("a", xsltOutputText("A"))
		anchor.Attributes = []xsltResultAttribute{{Name: xsltName{Local: "href"}, Value: " \t/é a?x=1&y=2"}, {Name: xsltName{Local: "title"}, Value: `a"b`}, {Name: xsltName{Local: "data"}, Value: "&{x}"}}
		tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("html", xsltOutputElement("head"), xsltOutputElement("body", anchor))}}
		props := xsltOutputMethod("html")
		props.IndentSet = true
		xsltOutputAssert(t, tree, props, "<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\"></head><body><a href=\"/%C3%A9%20a?x=1&amp;y=2\" title='a\"b' data=\"&{x}\">A</a></body></html>\n")
		meta := xsltOutputElement("meta")
		meta.Attributes = []xsltResultAttribute{{Name: xsltName{Local: "http-equiv"}, Value: "content-type"}, {Name: xsltName{Local: "content"}, Value: "something; charset=ASCII"}}
		tree.Children[0].Children[0].Children = []*xsltResultNode{meta}
		props.Encoding = "UTF-8"
		props.EncodingSet = true
		xsltOutputAssert(t, tree, props, "<html><head><meta http-equiv=\"content-type\" content=\"text/html; charset=UTF-8\"></head><body><a href=\"/%C3%A9%20a?x=1&amp;y=2\" title='a\"b' data=\"&{x}\">A</a></body></html>\n")
		if meta.Attributes[1].Value != "something; charset=ASCII" {
			t.Fatal("saver mutated result META")
		}
		tree = &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("html", xsltOutputElement("head", xsltOutputElement("title", xsltOutputText("T"))), xsltOutputElement("body"))}}
		props = xsltOutputMethod("html")
		xsltOutputAssert(t, tree, props, "<html>\n<head>\n<meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\">\n<title>T</title>\n</head>\n<body></body>\n</html>\n")
		span := xsltOutputElement("span")
		span.Attributes = []xsltResultAttribute{{Name: xsltName{Local: "data"}, Value: "<!--keep <&> -->&{x<&}"}}
		props.IndentSet = true
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{span}}, props, "<span data=\"<!--keep <&> -->&{x<&}\"></span>\n")
	})
	t.Run("FullEncodedDatum", func(t *testing.T) {
		// UTF16 full datums, including terminal NUL bytes, were independently
		// observed by the pinned public materialized octet_length/textsend witness.
		for _, tc := range []struct{ encoding, text, hex string }{
			{"UTF-16", "あé", "fffe4230e900"}, {"UTF-16LE", "あé", "4230e900"}, {"UTF-16BE", "あé", "304200e9"},
			{"UTF-16", "😀", "fffe3dd800de"}, {"UTF-16LE", "😀", "3dd800de"}, {"UTF-16BE", "😀", "d83dde00"},
			{"UTF-32", "😀", "fffe000000f60100"}, {"UTF-32BE", "😀", "0001f600"},
			{"UCS-4", "😀", "0001f600"}, {"UCS-2BE", "é", "00e9"},
			// Report16 materialized octet_length/textsend observed these exact
			// terminal shifts, before the output buffer's final iconv reset.
			{"ISO-2022-JP-2", "é", "1b2428442b31"},
			{"ISO-2022-KR", "한", "1b2429430e4751"},
			{"ISO-2022-CN", "中", "1b2429410e5650"},
			{"UTF-7", "é", "2b414f"},
			{"ISO-2022-JP-2", "éA", "1b2428442b311b284241"},
			{"ISO-2022-JP-2", "한A", "1b24284347511b284241"},
			{"ISO-2022-JP-2", "ｶA", "1b2849361b284241"},
			{"UTF-7-IMAP", "&éA", "262d26414f6b2d41"},
			{"ISO-8859-1", "あé", "262331323335343be9"},
			{"ISO-2022-JP", "あé", "1b244224221b284226233233333b"},
			{"ISO-2022-KR", "한A", "1b2429430e47510f41"},
			{"ISO-2022-CN", "中A", "1b2429410e56500f41"},
			{"UTF-7", "éA", "2b414f6b2d41"},
			{"ASCII", "é😀", "26233233333b26233132383531323b"},
			{"WINDOWS-1252", "€é", "80e9"}, {"GB18030", "😀", "9439fc36"},
		} {
			t.Run(tc.encoding+tc.text, func(t *testing.T) {
				want, err := hex.DecodeString(tc.hex)
				if err != nil {
					t.Fatal(err)
				}
				props := xsltOutputMethod("text")
				props.Encoding = tc.encoding
				props.EncodingSet = true
				xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText(tc.text)}}, props, string(want))
			})
		}
	})
	t.Run("StateAndLookahead", func(t *testing.T) {
		props := xsltOutputMethod("text")
		props.Encoding = "EUC-JISX0213"
		props.EncodingSet = true
		// JIS X0213 combined mapping: U+304B U+309A is one EUC character,
		// including when the scalars are supplied by separate result text nodes.
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("か"), xsltOutputText("\u309a")}}, props, "\xa4\xf7")
		props.Encoding = "ISO-2022-JP"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("あ"), xsltOutputText("A"), xsltOutputText("あ"), xsltOutputText("B")}}, props, "\x1b$B$\"\x1b(BA\x1b$B$\"\x1b(BB")
		// Memory saver copies before output-buffer close/reset. Neither a reset nor
		// pending UTF7 pad bits are invented after that actual publication boundary.
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("あ")}}, props, "\x1b$B$\"")
		props.Encoding = "UTF-7"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("é")}}, props, "+AO")
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("+é A")}}, props, "+-+AOk A")
		props.Encoding = "ISO-2022-CN"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("中\n中A")}}, props, "\x1b$)A\x0eVP\x0f\n\x1b$)A\x0eVP\x0fA")
		props.Encoding = "ISO-2022-JP"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("¥A\n")}}, props, "\x1b(J\\A\x1b(B\n")
		props.Encoding = "ISO-2022-JP-2"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("\u0080\n\u0080A")}}, props, "\x1b.A\x1bN\x00\n\x1b.A\x1bN\x00A")
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("\U000e0001\U000e006b\U000e006f한A")}}, props, "\x1b$(CGQ\x1b(BA")
		props.Encoding = "HTML"
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("éα€😀")}}, props, "&eacute;&alpha;&euro;&#128512;")
	})
	t.Run("ShiftPublicationBoundaries", func(t *testing.T) {
		for _, tc := range []struct{ encoding, text, terminal, ascii string }{
			{"ISO-2022-JP-2", "é", "\x1b$(D+1", "\x1b$(D+1\x1b(BA"},
			{"ISO-2022-KR", "한", "\x1b$)C\x0eGQ", "\x1b$)C\x0eGQ\x0fA"},
			{"ISO-2022-CN", "中", "\x1b$)A\x0eVP", "\x1b$)A\x0eVP\x0fA"},
			{"UTF-7", "é", "+AO", "+AOk-A"},
		} {
			t.Run(tc.encoding, func(t *testing.T) {
				props := xsltOutputMethod("text")
				props.Encoding, props.EncodingSet = tc.encoding, true
				// Empty contributions and ignored non-text nodes are real
				// result boundaries, not encoder close/reset operations.
				tree := &xsltResultTree{Children: []*xsltResultNode{xsltOutputText(""), xsltOutputElement("e", xsltOutputText(tc.text)), {Kind: xsltResultComment, Value: "ignored"}, xsltOutputText("")}}
				xsltOutputAssert(t, tree, props, tc.terminal)
				tree.Children = append(tree.Children, xsltOutputText("A"))
				xsltOutputAssert(t, tree, props, tc.ascii)
				// Empty/new invocations cannot inherit the previous shift.
				xsltOutputAssert(t, &xsltResultTree{}, props, "")
				xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText(tc.text)}}, props, tc.terminal)
			})
		}
	})
	t.Run("UnknownLabelRecovery", func(t *testing.T) {
		props := xsltOutputProperties{Encoding: "not-an-encoding", EncodingSet: true}
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("r", xsltOutputText("é"))}}, props, "<?xml version=\"1.0\" encoding=\"not-an-encoding\"?>\n<r>é</r>\n")
	})
	t.Run("CancellationAndLimits", func(t *testing.T) {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			for _, phase := range []string{"saveXSLT", "write", "scalar", "ensureBuilder"} {
				c := &phaseContext{Context: context.Background(), phase: phase, after: 8, cause: cause}
				props := xsltOutputMethod("text")
				props.Encoding = "UTF-16"
				props.EncodingSet = true
				got, err := saveXSLT(c, &xsltResultTree{Children: []*xsltResultNode{xsltOutputText(strings.Repeat("あ😀é", 10000))}}, props)
				if !c.admitted || !errors.Is(err, cause) || got != (XSLTResult{}) {
					t.Fatalf("phase %s admitted %t: %+v %v", phase, c.admitted, got, err)
				}
			}
		}
		p := &parser{ctx: context.Background()}
		out := xsltOutputEncoder{p: p, id: 6}
		if p.ensureBuilder(&out.b, math.MaxInt) || p.err == nil {
			t.Fatal("unchecked saver growth")
		}
		if out.b.Len() != 0 {
			t.Fatal("size rejection published bytes")
		}
		xsltOutputAssert(t, &xsltResultTree{Children: []*xsltResultNode{xsltOutputElement("r")}}, xsltOutputProperties{}, "<?xml version=\"1.0\"?>\n<r/>\n")
	})
	t.Run("Ownership", func(t *testing.T) {
		text := xsltOutputText("owned<&é")
		root := xsltOutputElement("r", text)
		tree := &xsltResultTree{Children: []*xsltResultNode{root}}
		props := xsltOutputProperties{Encoding: "UTF-8", EncodingSet: true}
		got := xsltOutputAssert(t, tree, props, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r>owned&lt;&amp;é</r>\n")
		backing := unsafe.StringData(got.Text)
		saved := strings.Clone(got.Text)
		text.Value = "changed"
		root.Name.Local = "changed"
		root.Children = nil
		tree.Children = nil
		props.Encoding = "UTF-16"
		if got.Text != saved || unsafe.StringData(got.Text) != backing {
			t.Fatal("published bytes retain mutable tree backing")
		}
		const workers = 6
		failures := make(chan error, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for worker := range workers {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				<-start
				for range 30 {
					encoding, want := "UTF-16BE", "\x30\x42\x00\xe9"
					if worker%2 != 0 {
						encoding, want = "ISO-2022-JP", "\x1b$B$\"\x1b(B&#233;"
					}
					props := xsltOutputMethod("text")
					props.Encoding = encoding
					props.EncodingSet = true
					result, err := saveXSLT(context.Background(), &xsltResultTree{Children: []*xsltResultNode{xsltOutputText("あé")}}, props)
					if err != nil || result.Null || result.Text != want {
						failures <- errors.New("saver invocation state leaked")
						return
					}
				}
			}(worker)
		}
		close(start)
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, XSLTResult{Text: saved}) || unsafe.StringData(got.Text) != backing {
			t.Fatal("later saves changed accepted bytes")
		}
	})
}
