package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestXSLTWhitespaceStages(t *testing.T) {
	// xsltApplyStripSpaces consults strip/preserve declarations, not xml:space.
	// String-values and axes must observe the same filtered document.
	body := `<xsl:value-of select="count(/r/text())"/>|<xsl:value-of select="count(/r/n/text())"/>|<xsl:value-of select="/r"/>`
	source := `<r xml:space="preserve"> <n xml:space="default"> </n> </r>`
	for _, tc := range []struct{ name, declarations, want string }{
		{"none", ``, "2|1|   "},
		{"strip", `<xsl:strip-space elements="*"/>`, "0|0|"},
		{"specific_preserve", `<xsl:strip-space elements="*"/><xsl:preserve-space elements="r"/>`, "2|0|  "},
		{"specific_strip", `<xsl:preserve-space elements="*"/><xsl:strip-space elements="r"/>`, "0|1| "},
		{"last_preserve", `<xsl:strip-space elements="r"/><xsl:preserve-space elements="r"/>`, "2|1|   "},
		{"last_strip", `<xsl:preserve-space elements="r"/><xsl:strip-space elements="r"/>`, "0|1| "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xsltExpectText(t, source, xsltTestStylesheet(tc.declarations, body), "", tc.want)
		})
	}
	xsltExpectText(t, `<r xml:space="preserve"> <n> A </n> </r>`, xsltTestStylesheet(`<xsl:strip-space elements="*"/>`, body), "", "0|1| A ")
	// Stylesheet whitespace preprocessing remains a separate stage which does
	// honor xml:space; source stripping must not alter the stylesheet rule.
	xsltExpectText(t, `<r/>`, xsltTestStylesheet(`<xsl:strip-space elements="*"/>`, `<out xml:space="preserve"> <xsl:text>X</xsl:text> </out>`), "", " X ")
}

func TestXSLTNamespaceStages(t *testing.T) {
	prefix := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"`
	for _, tc := range []struct{ name, root, declarations, body, want string }{
		{"local_exclusion_relocated", ``, ``, `<out xmlns:u="urn:unused" xsl:exclude-result-prefixes="u"/>`, "<out xmlns:u=\"urn:unused\"/>\n"},
		{"root_exclusion", ` xmlns:u="urn:unused" exclude-result-prefixes="u"`, ``, `<out/>`, "<out/>\n"},
		{"root_exclusion_relocated", ` xmlns:u="urn:unused" exclude-result-prefixes="u"`, ``, `<out xmlns:u="urn:unused"/>`, "<out/>\n"},
		{"required_excluded_name", ` xmlns:u="urn:used" exclude-result-prefixes="u"`, ``, `<u:out/>`, "<u:out xmlns:u=\"urn:used\"/>\n"},
		{"local_default_not_relocated", ``, ``, `<out xmlns="urn:default" xsl:exclude-result-prefixes="#default"/>`, "<out xmlns=\"urn:default\"/>\n"},
		{"alias_to_default", ``, `<xsl:namespace-alias xmlns:old="urn:old" xmlns="urn:new" stylesheet-prefix="old" result-prefix="#default"/>`, `<old:out xmlns:old="urn:old"/>`, "<old:out xmlns:old=\"urn:new\"/>\n"},
		{"alias_to_other_prefix", ``, `<xsl:namespace-alias xmlns:a="urn:old" xmlns:b="urn:new" stylesheet-prefix="a" result-prefix="b"/>`, `<p:out xmlns:p="urn:old" p:a="A" plain="B"/>`, "<p:out xmlns:p=\"urn:new\" p:a=\"A\" plain=\"B\"/>\n"},
		{"alias_to_absent_default", ``, `<xsl:namespace-alias xmlns:a="urn:old" stylesheet-prefix="a" result-prefix="#default"/>`, `<p:out xmlns:p="urn:old"/>`, "<out/>\n"},
		{"dynamic_name_not_aliased", ``, `<xsl:namespace-alias xmlns:a="urn:old" xmlns:b="urn:new" stylesheet-prefix="a" result-prefix="b"/>`, `<xsl:element name="p:out" namespace="urn:old"/>`, "<p:out xmlns:p=\"urn:old\"/>\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			style := prefix + tc.root + `><xsl:output omit-xml-declaration="yes"/>` + tc.declarations + `<xsl:template match="/">` + tc.body + `</xsl:template></xsl:stylesheet>`
			xsltExpectText(t, `<r/>`, style, "", tc.want)
		})
	}
	style := prefix + `><xsl:template match="/"><out xmlns:u="urn:unused" xsl:exclude-result-prefixes="u"/></xsl:template></xsl:stylesheet>`
	program := xsltTestProgram(t, style)
	literal := program.templates[0].instruction.children[0]
	if len(literal.source.namespaces) != 0 || literal.namespaces["u"] != "urn:unused" || len(program.root.source.namespaces) != 2 || program.root.source.namespaces[0].value != "urn:unused" {
		t.Fatal("local exclusion must relocate the real declaration before inherited namespace construction")
	}
}

func TestXSLTPatternStages(t *testing.T) {
	for _, pattern := range []string{"[", "n[", "n[1", "n['unterminated]", "n/", "n|", "q:n", "self::n", "n[1]/["} {
		xsltExpectError(t, `<r/>`, xsltTestStylesheet(`<xsl:template match="`+pattern+`">X</xsl:template>`, ""), "", XSLTCompile, "")
	}
	// A balanced predicate does invoke XPath compilation. Its callback is real,
	// unlike the match grammar's missing-name or missing-bracket reports.
	for _, pattern := range []string{"n[1+]", "n[]", "n[/*/name()]"} {
		xsltExpectError(t, `<r/>`, xsltTestStylesheet(`<xsl:template match="`+pattern+`">X</xsl:template>`, ""), "", XSLTCompile, "Invalid expression")
	}
	xsltExpectError(t, `<r/>`, xsltTestStylesheet("", `<xsl:value-of select="["/>`), "", XSLTCompile, "Invalid expression")
	// Ordered earlier reader callbacks survive generic match compilation failure.
	prefix := "input conversion failed due to input error, bytes 0xC3 0xA9 0x41 0x42\ninput conversion failed due to input error, bytes 0xC3 0xA9 0x41 0x42\nencoder error"
	source := `<?xml version="1.0" encoding="ISO-2022-KR"?><r/>éABCD`
	xsltExpectError(t, source, xsltTestStylesheet(`<xsl:template match="["/>`, ""), "", XSLTCompile, prefix)
	xsltExpectError(t, source, xsltTestStylesheet(`<xsl:template match="n[1+]"/>`, ""), "", XSLTCompile, prefix+"\nInvalid expression")
	xsltExpectError(t, `<r/>`, xsltTestStylesheet(`<xsl:template match="n[$v]"/>`, ""), "", XSLTCompile, "Forbidden variable")
	// NOVAR is a predicate compiler flag, not a global lexer policy.
	if _, err := compileXPath(context.Background(), "$v"); err != nil {
		t.Fatal("ordinary XPath variable compilation changed", err)
	}
	patterns, err := compileXSLTNumberPattern(context.Background(), "n[$v]", nil)
	if err != nil || len(patterns) != 1 {
		t.Fatal("number patterns must retain their variable-permitting compiler", patterns, err)
	}

	ns := map[string]string{"p": "urn:p"}
	for _, tc := range []struct {
		pattern  string
		priority float64
	}{
		{"n", 0}, {"p:*", -0.25}, {"@*", -0.5}, {"node()", -0.5},
		{"processing-instruction('p')", 0}, {"//n", 0.5}, {"n[2]", 0.5},
	} {
		patterns, err := compileXSLTPattern(context.Background(), tc.pattern, ns, false)
		if err != nil || len(patterns) != 1 || patterns[0].priority != tc.priority {
			t.Fatalf("pattern %q: %+v %v", tc.pattern, patterns, err)
		}
	}
	xsltExpectText(t, `<r><n>A</n><n>B</n></r>`, xsltTestStylesheet(`<xsl:template match="child::n[position()=2]">[<xsl:value-of select="."/>]</xsl:template>`, `<xsl:apply-templates select="/r/n"/>`), "", "A[B]")
	xsltExpectText(t, `<r><n x="[a]"/><n/></r>`, xsltTestStylesheet(`<xsl:template match="n[@x='[a]']">P</xsl:template><xsl:template match="n">N</xsl:template>`, `<xsl:apply-templates select="/r/n"/>`), "", "PN")
}

func TestXSLTPatternStagesCancellation(t *testing.T) {
	ctx := &phaseContext{Context: context.Background(), phase: "name", after: 4, cause: context.Canceled}
	patterns, err := compileXSLTPattern(ctx, strings.Repeat("n", 10000), nil, false)
	if !ctx.admitted || !errors.Is(err, context.Canceled) || patterns != nil {
		t.Fatalf("pattern cancellation admitted=%v patterns=%+v error=%v", ctx.admitted, patterns, err)
	}
	xsltExpectText(t, `<r/>`, xsltTestStylesheet("", "healthy"), "", "healthy")
}
