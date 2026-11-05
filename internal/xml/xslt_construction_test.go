package xml

import (
	"context"
	"testing"
)

func xsltConstructTree(t *testing.T, source, style string) *xsltResultTree {
	t.Helper()
	program := xsltTestProgram(t, style)
	_, doc, _, err := parseConfigured(context.Background(), source, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, legacyDiagnostics: true, xsltReader: true})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := applyXSLT(context.Background(), program, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
func TestXSLTConstruction(t *testing.T) {
	style := xsltTestStylesheet(`<xsl:param name="p" select="'X'"/><xsl:attribute-set name="s"><xsl:attribute name="x">1</xsl:attribute></xsl:attribute-set>`, `<out xsl:use-attribute-sets="s" x="2" a="{{{$p}}}"><xsl:element name="n"><xsl:attribute name="a">v</xsl:attribute><xsl:text disable-output-escaping="yes">&lt;raw&gt;</xsl:text></xsl:element><xsl:comment>C</xsl:comment><xsl:processing-instruction name="p">D</xsl:processing-instruction></out>`)
	tree := xsltConstructTree(t, "<r/>", style)
	if len(tree.Children) != 1 {
		t.Fatal(tree)
	}
	out := tree.Children[0]
	if out.Kind != xsltResultElement || out.Name.Local != "out" || len(out.Attributes) != 2 || out.Attributes[0].Name.Local != "x" || out.Attributes[0].Value != "2" || out.Attributes[1].Value != "{X}" {
		t.Fatal(out)
	}
	if len(out.Children) != 3 || out.Children[0].Kind != xsltResultElement || out.Children[0].Name.Local != "n" || out.Children[1].Kind != xsltResultComment || out.Children[1].Value != "C" || out.Children[2].Kind != xsltResultPI || out.Children[2].Name.Local != "p" {
		t.Fatal(out.Children)
	}
	n := out.Children[0]
	if len(n.Attributes) != 1 || n.Attributes[0].Value != "v" || len(n.Children) != 1 || n.Children[0].Value != "<raw>" || !n.Children[0].DisableEscaping {
		t.Fatal(n)
	}
	// Copy-of preserves every fragment child and DOE metadata, while copies own
	// their mutable attributes/namespaces/children rather than aliasing the RTF.
	tree = xsltConstructTree(t, "<r/>", xsltTestStylesheet("", `<xsl:variable name="v"><a>A</a><b>B</b><xsl:text disable-output-escaping="yes">&lt;x/&gt;</xsl:text></xsl:variable><xsl:copy-of select="$v"/><xsl:copy-of select="$v"/>`))
	if len(tree.Children) != 6 || tree.Children[0].Name.Local != "a" || tree.Children[1].Name.Local != "b" || tree.Children[2].Value != "<x/>" || !tree.Children[2].DisableEscaping || tree.Children[0] == tree.Children[3] || tree.Children[0].Children[0] == tree.Children[3].Children[0] {
		t.Fatal(tree.Children)
	}
	tree.Children[0].Children[0].Value = "changed"
	if tree.Children[3].Children[0].Value != "A" {
		t.Fatal("mutable copied result sharing")
	}
	tree = xsltConstructTree(t, `<r a="v"><n>A</n></r>`, xsltTestStylesheet("", `<xsl:for-each select="/r"><xsl:copy/><xsl:copy-of select="."/></xsl:for-each>`))
	if len(tree.Children) != 2 || len(tree.Children[0].Children) != 0 || len(tree.Children[0].Attributes) != 0 || len(tree.Children[1].Children) != 1 || len(tree.Children[1].Attributes) != 1 {
		t.Fatal("shallow/deep construction mismatch", tree.Children)
	}
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<out>T<xsl:attribute name="late">V</xsl:attribute></out>`), "", XSLTApply, "")
	xsltExpectText(t, "<r/>", xsltTestStylesheet(`<xsl:attribute-set name="a" use-attribute-sets="b"/><xsl:attribute-set name="b" use-attribute-sets="a"/>`, `<e xsl:use-attribute-sets="a"/>`), "", "")
}
func TestXSLTSortAndNumber(t *testing.T) {
	source := `<r><n v="10">a</n><n v="2">b</n><n v="2">c</n></r>`
	xsltExpectText(t, source, xsltTestStylesheet("", `<xsl:for-each select="/r/n"><xsl:sort select="@v" data-type="number"/><xsl:value-of select="."/></xsl:for-each>`), "", "bca")
	xsltExpectText(t, source, xsltTestStylesheet("", `<xsl:for-each select="/r/n"><xsl:sort select="@v" data-type="number"/><xsl:sort select="." order="descending"/><xsl:value-of select="."/></xsl:for-each>`), "", "cba")
	nanSource := `<r><n v="bad">a</n><n v="2">b</n><n v="bad">c</n><n v="1">d</n></r>`
	for _, tc := range []struct{ order, want string }{{"ascending", "acdb"}, {"descending", "bdac"}} {
		xsltExpectText(t, nanSource, xsltTestStylesheet("", `<xsl:for-each select="/r/n"><xsl:sort select="@v" data-type="number" order="`+tc.order+`"/><xsl:value-of select="."/></xsl:for-each>`), "", tc.want)
	}
	xsltExpectText(t, `<r><section><section/></section><section/></r>`, xsltTestStylesheet("", `<xsl:for-each select="//section"><xsl:number count="section" level="multiple" format="1.1"/>|</xsl:for-each>`), "", "1|1.1|2|")
	xsltExpectText(t, `<r><n/><group><n/></group><n/></r>`, xsltTestStylesheet("", `<xsl:for-each select="//n"><xsl:number count="n" level="any"/>|</xsl:for-each>`), "", "1|2|3|")
	xsltExpectText(t, "<r/>", xsltTestStylesheet(`<xsl:decimal-format name="e" decimal-separator="," grouping-separator="."/>`, `<xsl:value-of select="format-number(1234.5,'#.##0,00','e')"/>`), "", "1.234,50")
}
