package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func xpathSQL(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func xpathSingleton(path, document, mapping string) string {
	args := xpathSQL(path) + "," + xpathSQL(document) + "::xml"
	if mapping != "" {
		args += "," + mapping
	}
	return "SELECT (xpath(" + args + "))[1]::text AS v"
}
func TestReferenceXMLXPathResults(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			path, doc string
			want      []any
		}{
			{"/r/a", "<r><a>x</a><a>y</a></r>", []any{"<a>x</a>", "<a>y</a>"}},
			{"/r/a | /r/a", "<r><a>x</a><a>x</a></r>", []any{"<a>x</a>", "<a>x</a>"}},
			{"/r/a[2]", "<r><a>x</a><a>y</a></r>", []any{"<a>y</a>"}},
		} {
			q := "SELECT v::text AS v FROM unnest(xpath(" + xpathSQL(tc.path) + "," + xpathSQL(tc.doc) + "::xml)) AS t(v)"
			s := xpathStep(b, q, "TEXT", tc.want...)
			s.Bag = true
			fixture.SQLJSONBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ path, doc, want string }{
			{"/r/text()", `<r>&lt;&amp;&gt;&quot;&apos;&#xD;</r>`, "&lt;&amp;&gt;\"'&#x0d;"},
			{"/r/@a", `<r a="&lt;&amp;&gt;&quot;&apos;&#xD;"/>`, "&lt;&amp;&gt;\"'&#x0d;"},
			{"string(/r)", `<r>&lt;&amp;&gt;&quot;&apos;&#xD;</r>`, "&lt;&amp;&gt;\"'&#x0d;"},
			{"/r", `<r a="&lt;&amp;&gt;&quot;&apos;&#xD;">&lt;&amp;&gt;&quot;&apos;&#xD;</r>`, "<r a=\"&lt;&amp;&gt;&quot;'&#13;\">&lt;&amp;&gt;\"'&#13;</r>"},
			{"/r/text()[1]", "<r><![CDATA[a<&>]]>tail</r>", "<![CDATA[a<&>]]>"},
			{"/r/text()[2]", "<r><![CDATA[a<&>]]>tail</r>", "tail"},
			{"string(/r)", "<r><![CDATA[a<&>]]>tail</r>", "a&lt;&amp;&gt;tail"},
			{"/r/text()[1]", "<r>a<![CDATA[b]]>c</r>", "a"},
			{"/r/text()[2]", "<r>a<![CDATA[b]]>c</r>", "<![CDATA[b]]>"},
			{"/r/text()[3]", "<r>a<![CDATA[b]]>c</r>", "c"},
			{"/r/text()", "<r>a\r\nb\rc</r>", "a\nb\nc"},
			{"/r/@a", "<r a=\"a\r\nb\rc\"/>", "a b c"},
			{"/", "<?xml version=\"1.0\"?><!--before--><?p data?><r><![CDATA[a<&>]]>tail<!--inside--><?i x?></r><!--after-->", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!--before-->\n<?p data?>\n<r><![CDATA[a<&>]]>tail<!--inside--><?i x?></r>\n<!--after-->\n"},
			{"/", "<?xml version=\"1.0\"?><r/>", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"},
			{"/", "<r/>", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"},
			{"/r", "<r></r>", "<r/>"},
			{"/r/comment()", "<r><!--inside--></r>", "<!--inside-->"},
			{"/r", `<r xmlns:q="urn:q?x=1&amp;y=2"/>`, `<r xmlns:q="urn:q?x=1&#38;y=2"/>`},
			{"/r/processing-instruction()", "<r><?i x?></r>", "<?i x?>"},
			{"/r/a[3]/preceding-sibling::*[1]", "<r><a>1</a><a>2</a><a>3</a></r>", "<a>2</a>"},
			{"(/r/a[3]/preceding-sibling::*)[1]", "<r><a>1</a><a>2</a><a>3</a></r>", "<a>1</a>"},
			{"string-length('é𝄞') + count(/r/a)", "<r><a/></r>", "3"},
			{"boolean(/r/a[@n=2 and position()=2])", "<r><a/><a n=\"2\"/></r>", "true"},
		} {
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(tc.path, tc.doc, ""), "TEXT", tc.want))
		}
	})
}
func TestReferenceXMLXPathScalars(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ path, want string }{
			{"count(/missing)", "0"}, {"false()", "false"}, {`""`, ""}, {"count(/r/a)", "2"}, {"string(/r/a[1])", "x"},
			{"1 div 3", "0.3333333333333333"}, {"string(1 div 3)", "0.333333333333333"}, {"0 div 0", "NaN"}, {"1 div 0", "Infinity"}, {"-1 div 0", "-Infinity"}, {"-0", "-0"}, {"-0 div 1", "-0"}, {"string(-0)", "0"},
		} {
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(tc.path, "<r><a>x</a><a>y</a></r>", ""), "TEXT", tc.want))
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT xpath_exists("+xpathSQL(tc.path)+",'<r/>'::xml) AS v", "BOOL", true))
		}
		for _, digits := range []struct{ setting, want string }{{"0", "0.333333333333333"}, {"-3", "0.333333333333"}, {"3", "0.3333333333333333"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits="+digits.setting, 0))
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton("1 div 3", "<r/>", ""), "TEXT", digits.want))
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton("string(1 div 3)", "<r/>", ""), "TEXT", "0.333333333333333"))
			if digits.setting == "-3" {
				for _, special := range []struct{ path, want string }{{"0 div 0", "NaN"}, {"1 div 0", "Infinity"}, {"-1 div 0", "-Infinity"}, {"-0", "-0"}} {
					fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(special.path, "<r/>", ""), "TEXT", special.want))
				}
			}
		}
		for _, parameter := range []bool{false, true} {
			q := xpathSingleton("1 div 3", "<r/>", "")
			if parameter {
				q = "SELECT (xpath($1::text,'<r/>'::xml))[1]::text AS v"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			run := func(want string) {
				s := xpathStep(b, q, "TEXT", want)
				if parameter {
					s.Args = []any{"1 div 3"}
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			}
			run("0.3333333333333333")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=-3", 0))
			want := "0.3333333333333333"
			if parameter {
				want = "0.333333333333"
			}
			run(want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
			run(want)
			held.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET plan_cache_mode", 0))
		}
	})
}
