package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestXML2LegacySets(t *testing.T) {
	ctx := context.Background()
	const document = "<r><a>A<b>B</b>C</a><a>D&amp;E</a></r>"
	for _, test := range []struct {
		doc, path, root, item, separator, want string
		kind LegacySetKind
	}{
		{document, "/r/a", "", "", "", "<a>A<b>B</b>C</a><a>D&amp;E</a>", LegacyNodeset},
		{document, "/r/a", "", "i", "", "<i><a>A<b>B</b>C</a></i><i><a>D&amp;E</a></i>", LegacyNodeset},
		{document, "/r/a", "o", "i", "", "<o><i><a>A<b>B</b>C</a></i><i><a>D&amp;E</a></i></o>", LegacyNodeset},
		{document, "/r/a", "o", "", "", "<o><a>A<b>B</b>C</a><a>D&amp;E</a></o>", LegacyNodeset},
		{document, "/r/a", "", "", ",", "ABC,D&E", LegacyList},
		{document, "/r/a", "", "", "|", "ABC|D&E", LegacyList},
		{document, "/r/a", "", "", "", "ABCD&E", LegacyList},
		{"<r><a/><a>X</a><a/></r>", "/r/a", "", "", "雪&", "雪&X雪&", LegacyList},
		{"<r/>", "/missing", "o", "i", "", "<o></o>", LegacyNodeset},
		{"<r/>", "/missing", "", "", ",", "", LegacyList},
		{"<r><a/></r>", "/r/a", "", "", "", "<a/>", LegacyNodeset},
		{"<r><a/></r>", "/r/a", "", "", ",", "", LegacyList},
		{"<r><a/></r>", "/r/a", "x y", "i&", "", "<x y><i&><a/></i&></x y>", LegacyNodeset},
		{`<r a="A&amp;B"/>`, "/r/@a", "", "", "", ` a="A&amp;B"`, LegacyNodeset},
		{`<r a="A&amp;B"/>`, "/r/@a", "", "", ",", "A&B", LegacyList},
		{`<r xmlns:p="urn:p"><p:a/></r>`, "/*/*", "", "", "", "<p:a/>", LegacyNodeset},
		{`<r><p:a xmlns:p="urn:p"/></r>`, "/*/*", "", "", "", `<p:a xmlns:p="urn:p"/>`, LegacyNodeset},
		{`<r xmlns:p="urn:p"/>`, "/*/namespace::p", "", "i", "", "<i>urn:p</i>", LegacyNodeset},
		{"<r><![CDATA[A&B]]></r>", "/r/text()", "", "", "", "<![CDATA[A&B]]>", LegacyNodeset},
		{"<r><![CDATA[A&B]]></r>", "/r/text()", "", "", ",", "A&B", LegacyList},
		{"<r>A&amp;B<!--c--><?p d?><a>雪</a></r>", "/r/node()", "o", "i", "", "<o><i>A&amp;B</i><i><!--c--></i><i><?p d?></i><i><a>雪</a></i></o>", LegacyNodeset},
		{"<r>A&amp;B<!--c--><?p d?><a>雪</a></r>", "/r/node()", "", "", "|", "A&B|c|d|雪", LegacyList},
		{"<r><a>A</a></r>", "a", "", "", "", "<a>A</a>", LegacyNodeset},
		{"<r><a>A</a></r>", "r/a", "", "", "", "", LegacyNodeset},
		{document, "string(.)", "o", "i", "", "ABCD&E", LegacyNodeset},
		{document, "'<&'", "o", "i", "|", "<&", LegacyList},
		{"<r/>", "/", "", "", "", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n", LegacyNodeset},
		{"<?p before?><!--head--><r/><!--tail--><?q after?>", "/", "", "", "", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<?p before?>\n<!--head-->\n<r/>\n<!--tail-->\n<?q after?>\n", LegacyNodeset},
		{`<!DOCTYPE r [<!ENTITY e "X">]><r>&e;</r>`, "/", "", "", "", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE r [\n<!ENTITY e \"X\">\n]>\n<r>X</r>\n", LegacyNodeset},
		{`<!DOCTYPE r [<!ENTITY e "<a>X</a>">]><r>&e;</r>`, "/r/a", "", "", "", "<a>X</a>", LegacyNodeset},
		{`<!DOCTYPE r [<!ATTLIST r a CDATA "default">]><r/>`, "/r/@a", "", "", "", "", LegacyNodeset},
	} {
		got, err := XPathSet(ctx, test.doc, test.path, test.root, test.item, test.separator, test.kind)
		if err != nil || got != (LegacySetResult{Text: test.want}) {
			t.Fatalf("set conversion %+v: got=%+v err=%v", test, got, err)
		}
	}
	for _, kind := range []LegacySetKind{LegacyNodeset, LegacyList} {
		for _, test := range []struct{ path string; result int }{{"true()", 2}, {"count(/r/a)", 3}} {
			got, err := XPathSet(ctx, document, test.path, "o", "i", "|", kind)
			if err != nil || got != (LegacySetResult{Text: "<unsupported/>", Unsupported: test.result}) {
				t.Fatal("actual unsupported classification", test, got, err)
			}
		}
		for _, doc := range []string{"", "<r>", "<r>&missing;</r>", `<?xml version="1.0" encoding="NO-SUCH-ENCODING"?><r/>`} {
			got, err := XPathSet(ctx, doc, "[", "", "", "", kind)
			if err != nil || got != (LegacySetResult{Null: true}) {
				t.Fatal("parse-before-compile NULL", got, err)
			}
		}
		for _, path := range []string{"$v", "/p:r", "unknown()"} {
			got, err := XPathSet(ctx, "<r/>", path, "", "", "", kind)
			if err != nil || got != (LegacySetResult{Null: true}) {
				t.Fatal("evaluation NULL", got, err)
			}
		}
		for _, path := range []string{"[", "", " \t\n"} {
			got, err := XPathSet(ctx, "<r/>", path, "", "", "", kind)
			var legacy *LegacyXPathError
			if !errors.As(err, &legacy) || legacy.Detail != "Invalid expression" || got != (LegacySetResult{}) {
				t.Fatal("unwrapped compile error", got, err)
			}
		}
		_, err := XPathSet(ctx, "<p:r>8</p:r>", "[", "", "", "", kind)
		var legacy *LegacyXPathError
		if !errors.As(err, &legacy) || legacy.Detail != "line 1: Namespace prefix p on r is not defined\n<p:r>8</p:r>\n    ^\nInvalid expression" {
			t.Fatal("ordered parser/compiler events", err)
		}
		got, err := XPathSet(ctx, "<r><a>A</a></r>", "/r/a", "", "", "", kind)
		if err != nil || got.Null || got.Unsupported != 0 || got.Text == "" {
			t.Fatal("healthy independent invocation", got, err)
		}
	}
}

func TestXML2LegacySetsCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, test := range []struct {
			phase, doc, path, root, item, separator string
			kind LegacySetKind
		}{
			{"startRaw", "<r/>", ".", "", "", "", LegacyNodeset},
			{"text", "<r>" + strings.Repeat("x", 20000) + "</r>", ".", "", "", "", LegacyNodeset},
			{"project", "<r>" + strings.Repeat("<a>A</a>", 1000) + "</r>", ".", "", "", "", LegacyNodeset},
			{"next", "<r/>", strings.Repeat(" ", 20000) + ".", "", "", "", LegacyNodeset},
			{"axis", "<r>" + strings.Repeat("<a>A</a>", 1000) + "</r>", "//a", "", "", "", LegacyNodeset},
			{"sourceText", "<r>A<b>" + strings.Repeat("x", 20000) + "</b>C</r>", ".", "", "", "|", LegacyList},
			{"legacyDumpNode", "<r><a>A</a></r>", "/r/a", "", "", "", LegacyNodeset},
			{"escape", "<r a='A&amp;B'/>", "/r/@a", "", "", "", LegacyNodeset},
			{"legacyTag", "<r><a/></r>", "/r/a", strings.Repeat("o", 20000), "i", "", LegacyNodeset},
		} {
			ctx := &phaseContext{Context: context.Background(), phase: test.phase, after: 1, cause: cause}
			got, err := XPathSet(ctx, test.doc, test.path, test.root, test.item, test.separator, test.kind)
			if !ctx.admitted || !errors.Is(err, cause) || got != (LegacySetResult{}) {
				t.Fatal("set cancellation atomicity", test.phase, ctx.admitted, got, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary cause acquired SQLSTATE")
			}
			healthy, err := XPathSet(context.Background(), "<r><a>A</a></r>", "/r/a", "", "", "", LegacyNodeset)
			if err != nil || healthy != (LegacySetResult{Text: "<a>A</a>"}) {
				t.Fatal("healthy set call after cancellation", healthy, err)
			}
		}
	}
}
