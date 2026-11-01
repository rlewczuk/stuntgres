package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestXMLXPathResults(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		path, doc string
		want      []string
		numeric   bool
		number    float64
	}{
		{"/r/a | /r/a", "<r><a>x</a><a>x</a></r>", []string{"<a>x</a>", "<a>x</a>"}, false, 0},
		{"/missing", "<r/>", []string{}, false, 0},
		{`""`, "<r/>", []string{""}, false, 0},
		{"false()", "<r/>", []string{"false"}, false, 0},
		{"1 div 3", "<r/>", nil, true, 1.0 / 3},
		{"/r/@a", `<r a="&lt;&amp;&gt;&quot;&#13;"/>`, []string{"&lt;&amp;&gt;\"&#x0d;"}, false, 0},
		{"/r/text()[1]", "<r><![CDATA[a<&>]]>tail</r>", []string{"<![CDATA[a<&>]]>"}, false, 0},
		{"/", "<r/>", []string{"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"}, false, 0},
		{"/", "<?xml version=\"1.0\"?><r/>", []string{"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"}, false, 0},
		{"/", "<?xml version=\"1.0\"?><!--before--><?p data?><r><![CDATA[a<&>]]>tail<!--inside--><?i x?></r><!--after-->", []string{"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!--before-->\n<?p data?>\n<r><![CDATA[a<&>]]>tail<!--inside--><?i x?></r>\n<!--after-->\n"}, false, 0},
		{"/r/namespace::q", `<r xmlns:q="urn:q?x=1&amp;y=2"/>`, []string{"urn:q?x=1&amp;#38;y=2"}, false, 0},
		{"/r", `<r xmlns:q="urn:q?x=1&amp;y=2"/>`, []string{`<r xmlns:q="urn:q?x=1&#38;y=2"/>`}, false, 0},
	} {
		source := accept(t, tc.doc, Content)
		got, _, err := XPath(ctx, tc.path, source, nil, false)
		if err != nil || !reflect.DeepEqual(got.Items, tc.want) || got.Numeric != tc.numeric || got.Number != tc.number {
			t.Fatal(tc.path, got, err)
		}
		exists, _, err := XPath(ctx, tc.path, source, nil, true)
		if err != nil || exists.Exists != (tc.numeric || len(tc.want) != 0) || exists.Items != nil || exists.Numeric {
			t.Fatal("cardinality-only", exists, err)
		}
		if source.Original() != tc.doc {
			t.Fatal("source mutated")
		}
	}
}
func TestXMLXPathNamespaces(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		path, doc string
		maps      []XPathMapping
		want      bool
	}{
		{"/q:r", `<r xmlns="urn:b"/>`, []XPathMapping{{Alias: "q", URI: "urn:a"}, {Alias: "q", URI: "urn:b"}}, true},
		{"/q:r", "<r/>", []XPathMapping{{Alias: "q"}}, false},
		{"/q:r", `<r xmlns=""/>`, []XPathMapping{{Alias: "q"}}, false},
		{"//q:r", `<z xmlns="urn:x"><r xmlns=""/></z>`, []XPathMapping{{Alias: "q"}}, false},
		{`/r/namespace::*[name()=""]`, `<r xmlns=""/>`, nil, true},
		{`/r/namespace::*[name()=""]`, `<r/>`, nil, false},
		{"/r/@xml:lang", `<r xml:lang="en"/>`, []XPathMapping{{Alias: "xml", URI: "wrong"}}, true},
	} {
		got, _, err := XPath(ctx, tc.path, accept(t, tc.doc, Content), tc.maps, true)
		if err != nil || got.Exists != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, pair := range []XPathMapping{{Alias: "", URI: "urn:x"}, {Alias: "q", Null: true}} {
		_, _, err := XPath(ctx, "[", accept(t, "<r/>", Content), []XPathMapping{pair}, false)
		var ns *XPathNamespaceError
		if !errors.As(err, &ns) || ns.Null != pair.Null {
			t.Fatal(err)
		}
	}
}
func TestXMLXPathCancellation(t *testing.T) {
	source := accept(t, "<r a=\""+strings.Repeat("&amp;", 2000)+"\">"+strings.Repeat("&lt;", 2000)+"<a/><a/></r>", Content)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, tc := range []struct {
			phase, path string
			maps        []XPathMapping
			after       int
			final       bool
		}{
			{"project", "//a", nil, 2, false},
			{"compileXPath", "//a", nil, 1, false},
			{"eval", "//a", nil, 2, false},
			{"registerXPathMappings", "//a", []XPathMapping{{Alias: "q", URI: "urn:x"}, {Alias: "q", URI: "urn:y"}}, 2, false},
			{"xpathEscape", "/r/@a", nil, 2, false},
			{"node", "/r", nil, 2, false},
			{"node", "/", nil, 2, false},
			{"xpathEscape", "/r/text()", nil, 1, true},
		} {
			ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: tc.after, cause: cause, final: tc.final}
			result, _, err := XPath(ctx, tc.path, source, tc.maps, false)
			if !ctx.admitted || !errors.Is(err, cause) || result.Items != nil || result.Numeric || result.Exists {
				t.Fatalf("%s: admitted %v result %+v err %v", tc.phase, ctx.admitted, result, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary cancellation gained SQLSTATE")
			}
			healthy, _, err := XPath(context.Background(), "count(/r/a)", source, nil, false)
			if err != nil || !healthy.Numeric || healthy.Number != 2 {
				t.Fatal("healthy reuse", healthy, err)
			}
		}
	}
	// Existence evaluation never enters the saver, even for a huge node result.
	for _, path := range []string{"/r", "/"} {
		ctx := &phaseContext{Context: context.Background(), phase: "serializeNode", after: 1, cause: context.Canceled}
		result, _, err := XPath(ctx, path, source, nil, true)
		if err != nil || ctx.admitted || !result.Exists || result.Items != nil {
			t.Fatal("existence serialized", path, result, err)
		}
	}
}
