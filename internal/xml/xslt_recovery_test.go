package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestXSLTRecovery(t *testing.T) {
	// The two-variable cycle is independently observed in the pinned release.
	// Additional dependency shapes distinguish evaluation/lookup propagation
	// from a fixed diagnostic count attached to a detected cycle.
	for _, tc := range []struct {
		name, declarations, detail string
	}{
		{"self", `<xsl:variable name="a" select="$a"/>`, "Undefined variable"},
		{"pair", `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="$a"/>`, "Undefined variable\nUndefined variable\nUndefined variable\nUndefined variable"},
		{"chain_missing", `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="$missing"/>`, "Undefined variable\nUndefined variable\nUndefined variable"},
		{"triangle", `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="$c"/><xsl:variable name="c" select="$a"/>`, strings.TrimSuffix(strings.Repeat("Undefined variable\n", 9), "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xsltExpectError(t, "<r/>", xsltTestStylesheet(tc.declarations, `<xsl:value-of select="$a"/>`), "", XSLTApply, tc.detail)
		})
	}
	// An overridden param breaks the real dependency graph, and an acyclic
	// dependency is evaluated once and shared without changing root context.
	declarations := `<xsl:param name="a" select="$b"/><xsl:variable name="b" select="$a"/>`
	xsltExpectText(t, "<r/>", xsltTestStylesheet(declarations, `<xsl:value-of select="$b"/>`), "a=7", "7")
	declarations = `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="concat(name(/*),position(),last())"/>`
	xsltExpectText(t, "<r/>", xsltTestStylesheet(declarations, `<xsl:for-each select="/r"><xsl:value-of select="$a"/>|<xsl:value-of select="$b"/></xsl:for-each>`), "", "r11|r11")

	for _, output := range []string{`<xsl:output method="text"/>`, `<xsl:output omit-xml-declaration="yes"/>`} {
		for _, body := range []string{
			`<e>T<xsl:attribute name="a">V</xsl:attribute></e>`,
			`<xsl:comment>A--B</xsl:comment>T`,
			`<xsl:comment>A-</xsl:comment>`,
			`<xsl:processing-instruction name="p">A?&gt;B</xsl:processing-instruction>`,
		} {
			style := strings.Replace(xsltTestStylesheet("", body), `<xsl:output method="text"/>`, output, 1)
			xsltExpectError(t, "<r/>", style, "", XSLTApply, "")
		}
	}
	// A generic transform ERROR continues the constructor, so a later actual
	// XPath failure is retained, but no partial result is published.
	for _, body := range []string{
		`<xsl:comment>A--B</xsl:comment>`,
		`<xsl:processing-instruction name="p">A?&gt;B</xsl:processing-instruction>`,
		`<e>T<xsl:attribute name="late">V</xsl:attribute></e>`,
		`<xsl:value-of select="document('file:///stuntgres-xslt-no-such-file.xml')"/>`,
	} {
		xsltExpectError(t, "<r/>", xsltTestStylesheet("", body+`<xsl:value-of select="$missing"/>`), "", XSLTApply, "Undefined variable")
	}
	for _, tc := range []struct {
		body, params string
		stage        XSLTStage
	}{
		{`<e a="{"/>`, "", XSLTCompile},
		{`<xsl:element name="1bad"/>`, "", XSLTCompile},
		{`<xsl:element name="{'1bad'}"/>`, "", XSLTApply},
		{`<xsl:element name="{$p}"/>`, "p='bad name'", XSLTApply},
		{`<xsl:for-each select="r/n"><xsl:sort select="." order="{$p}"/></xsl:for-each>`, "p='sideways'", XSLTApply},
		{`<xsl:for-each select="r/n"><xsl:sort select="." data-type="{$p}"/></xsl:for-each>`, "p='mystery'", XSLTApply},
	} {
		xsltExpectError(t, "<r><n>B</n><n>A</n></r>", xsltTestStylesheet(`<xsl:param name="p"/>`, tc.body), tc.params, tc.stage, "")
	}
	// The release creates a PI whose target is xml, without a PITarget check.
	body := `<xsl:processing-instruction name="xml">D</xsl:processing-instruction>T`
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", body), "", "T")
	style := strings.Replace(xsltTestStylesheet("", body), `<xsl:output method="text"/>`, `<xsl:output omit-xml-declaration="yes"/>`, 1)
	xsltExpectText(t, "<r/>", style, "", "<?xml D?>T\n")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:value-of select="concat('[',system-property('xsl:version'),']')"/>|<xsl:value-of select="system-property('xsl:version')+1"/>|<xsl:value-of select="system-property('xsl:version')='1'"/>`), "", "[1.0]|2|false")

	// Duplicate named declarations return before their symbol fields are read.
	declarations = `<xsl:decimal-format name="d" decimal-separator="."/><xsl:decimal-format name="d" decimal-separator=","/>`
	xsltExpectText(t, "<r/>", xsltTestStylesheet(declarations, `<xsl:value-of select="format-number(1.5,'0.0','d')"/>`), "", "1.5")
	declarations = `<xsl:decimal-format name="d"/><xsl:decimal-format name="d" zero-digit="invalid"/>`
	xsltExpectText(t, "<r/>", xsltTestStylesheet(declarations, `<xsl:value-of select="format-number(1.5,'0.0','d')"/>`), "", "1.5")
	// Unnamed declarations update their existing format, preserving unspecified
	// fields instead of accidentally resetting them to the initial defaults.
	declarations = `<xsl:decimal-format minus-sign="M"/><xsl:decimal-format NaN="N"/>`
	xsltExpectText(t, "<r/>", xsltTestStylesheet(declarations, `<xsl:value-of select="format-number(-2,'0')"/>|<xsl:value-of select="format-number(number('x'),'0')"/>`), "", "M2|N")
}

func TestXSLTAttributeSetRecovery(t *testing.T) {
	emptyCycle := `<xsl:attribute-set name="a" use-attribute-sets="b"/><xsl:attribute-set name="b" use-attribute-sets="a"/>`
	style := xsltTestStylesheet(emptyCycle, `<e xsl:use-attribute-sets="a"/>`)
	xsltExpectText(t, "<r/>", style, "", "")
	tree := xsltConstructTree(t, "<r/>", style)
	if len(tree.Children) != 1 || tree.Children[0].Kind != xsltResultElement || tree.Children[0].Name.Local != "e" || len(tree.Children[0].Attributes) != 0 {
		t.Fatal("cycle recovery discarded or fabricated the constructed element", tree)
	}
	// Recovery retains real attributes while removing recursive dependency
	// edges. The caller's own x wins over the inherited duplicate x.
	declarations := `<xsl:attribute-set name="a" use-attribute-sets="b"><xsl:attribute name="x">A</xsl:attribute></xsl:attribute-set><xsl:attribute-set name="b" use-attribute-sets="a"><xsl:attribute name="y">B</xsl:attribute><xsl:attribute name="x">other</xsl:attribute></xsl:attribute-set>`
	style = xsltTestStylesheet(declarations, `<e xsl:use-attribute-sets="a"/>`)
	program := xsltTestProgram(t, style)
	if len(program.attributeSets["\x00a"]) != 2 {
		t.Fatal("compiled cycle lost its real attributes")
	}
	for _, attribute := range program.attributeSets["\x00a"] {
		if attribute.op != "attribute" {
			t.Fatal("unresolved declaration escaped into executable attribute set")
		}
	}
	tree = xsltConstructTree(t, "<r/>", style)
	attributes := tree.Children[0].Attributes
	if len(attributes) != 2 || attributes[0].Name.Local != "x" || attributes[0].Value != "A" || attributes[1].Name.Local != "y" || attributes[1].Value != "B" {
		t.Fatal("resolved cycle lost attribute identity, value or precedence", attributes)
	}
	// Unknown references have no declaration to merge; they do not fabricate a
	// runtime failure or hide the actual children of the literal result element.
	xsltExpectText(t, "<r/>", xsltTestStylesheet(`<xsl:attribute-set name="a" use-attribute-sets="missing"/>`, `<e xsl:use-attribute-sets="a">T</e>`), "", "T")
}

func TestXSLTRecoveryCancellation(t *testing.T) {
	for _, tc := range []struct{ phase, declarations string }{
		{"globalValue", `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="$a"/>`},
		{"resolveAttributeSets", `<xsl:attribute-set name="a" use-attribute-sets="b"><xsl:attribute name="x">A</xsl:attribute></xsl:attribute-set><xsl:attribute-set name="b" use-attribute-sets="a"/>`},
	} {
		ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: 2, cause: context.Canceled}
		result, err := ProcessXSLT(ctx, "<r/>", xsltTestStylesheet(tc.declarations, `<e xsl:use-attribute-sets="a"/>`), "")
		if !ctx.admitted || !errors.Is(err, context.Canceled) || result != (XSLTResult{}) {
			t.Fatalf("phase %s admitted=%v result=%+v error=%v", tc.phase, ctx.admitted, result, err)
		}
		xsltExpectText(t, "<r/>", xsltTestStylesheet("", "healthy"), "", "healthy")
	}
}
