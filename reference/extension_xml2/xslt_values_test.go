package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTValues(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ name, source, declarations, body, want string }{
			{"instruction_current", `<r><n ref="b"/><v id="a">A</v><v id="b">B</v></r>`, ``, `<xsl:for-each select="r/n"><xsl:value-of select="../v[@id=current()/@ref]"/></xsl:for-each>`, "B"},
			{"lexical_namespaces", `<r xmlns:q="urn:q"><q:n>A</q:n><n>B</n></r>`, ``, `<xsl:value-of xmlns:p="urn:q" select="/r/p:n"/>|<xsl:value-of select="/r/n"/>`, "A|B"},
			{"keys_nodeset_values", `<r><n id="a">A</n><n id="b">B</n><ref>a</ref><ref>b</ref></r>`, `<xsl:key name="k" match="n" use="@id"/>`, `<xsl:value-of select="key('k','b')"/>|<xsl:value-of select="count(key('k',/r/ref))"/>|<xsl:value-of select="generate-id(key('k','b'))=generate-id(/r/n[2])"/>`, "B|2|true"},
			{"key_multiple_declarations", `<r><n id="a">A</n><m id="a">B</m></r>`, `<xsl:key name="k" match="n" use="@id"/><xsl:key name="k" match="m" use="@id"/>`, `<xsl:for-each select="key('k','a')"><xsl:value-of select="."/></xsl:for-each>`, "AB"},
			{"rtf_all_children", `<r/>`, ``, `<xsl:variable name="v"><a>A</a><b>B</b></xsl:variable><xsl:value-of select="string($v)"/>|<xsl:value-of select="boolean($v)"/>`, "AB|true"},
			{"empty_string_and_empty_rtf", `<r/>`, ``, `<xsl:variable name="s"/><xsl:variable name="r"><xsl:if test="false()">X</xsl:if></xsl:variable><xsl:value-of select="boolean($s)"/>|<xsl:value-of select="boolean($r)"/>|<xsl:value-of select="number($s)"/>`, "false|true|NaN"},
			{"mixed_input_nodes", `<r>a<![CDATA[b]]>c</r>`, ``, `<xsl:value-of select="count(/r/text())"/>:<xsl:value-of select="/r"/>`, "3:abc"},
			{"format_number", `<r/>`, ``, `<xsl:value-of select="format-number(1234.5,'#,##0.00')"/>|<xsl:value-of select="format-number(-2.5,'0.0;(0.0)')"/>|<xsl:value-of select="format-number(0.125,'0%')"/>`, "1,234.50|(2.5)|13%"},
			{"named_decimal_format", `<r/>`, `<xsl:decimal-format name="e" decimal-separator="," grouping-separator="."/>`, `<xsl:value-of select="format-number(1234.5,'#.##0,00','e')"/>`, "1.234,50"},
			{"availability", `<r/>`, ``, `<xsl:value-of xmlns="http://www.w3.org/1999/XSL/Transform" select="element-available('value-of')"/>|<xsl:value-of select="function-available('string')"/>|<xsl:value-of select="function-available('not-a-function')"/>`, "true|true|false"},
			{"unparsed_entity", `<!DOCTYPE r [<!NOTATION bin SYSTEM "bin"><!ENTITY image SYSTEM "urn:image" NDATA bin>]><r/>`, ``, `<xsl:value-of select="unparsed-entity-uri('image')"/>|<xsl:value-of select="unparsed-entity-uri('missing')"/>`, "urn:image|"},
			{"generated_identity", `<r><n/><n/></r>`, ``, `<xsl:value-of select="generate-id(r/n[1])=generate-id(r/n[1])"/>|<xsl:value-of select="generate-id(r/n[1])=generate-id(r/n[2])"/>|<xsl:value-of select="generate-id(r/missing)"/>`, "true|false|"},
			{"xpath_axes_and_core", `<r><n>A</n><n>B</n><n>C</n></r>`, ``, `<xsl:for-each select="r/n[2]"><xsl:value-of select="concat(preceding-sibling::n,following-sibling::n,substring('abcd',2,2),translate('aba','ab','XY'))"/></xsl:for-each>`, "ACbcXYX"},
			{"system_version_kind", `<r/>`, ``, `<xsl:value-of select="concat('[',system-property('xsl:version'),']')"/>|<xsl:value-of select="system-property('xsl:version')+1"/>|<xsl:value-of select="system-property('xsl:version')='1'"/>`, "[1.0]|2|false"},
			{"stylesheet_compiled_tree", `<r/>`, `<d:data xmlns:d="urn:d"><!--foreign--></d:data>`, `<!--template--><xsl:text>T</xsl:text><xsl:value-of select="count(document('')//xsl:text)"/>|<xsl:value-of select="count(document('')//comment())"/>|<xsl:value-of select="name(document('')/*)"/>`, "T0|1|xsl:stylesheet"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xsltExample(t, p, b, c, tc.source, xsltSmokeStyle(xsltTextOutput, tc.declarations, tc.body), tc.want)
			})
		}
		// XSLT system properties identify the genuine provider, not an emulated
		// libxslt identity. PG constants are released libxslt/v1.1.35/xslt.h.
		provider := "1.0|stuntgres|https://github.com/rlewczuk/stuntgres"
		if b.PG {
			provider = "1.0|libxslt|http://xmlsoft.org/XSLT/"
		}
		xsltExample(t, p, b, c, `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="system-property('xsl:version')"/>|<xsl:value-of select="system-property('xsl:vendor')"/>|<xsl:value-of select="system-property('xsl:vendor-url')"/>`), provider)
		// Report7 source-first and report9 stylesheet-first are deliberately both
		// retained: the stylesheet XPath owner is real, while released document-info
		// lookup aliases its key cache to the transform source document.
		root := `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:d="urn:d">` + xsltTextOutput + `<d:k>S</d:k><xsl:key name="k" match="s|d:k" use="'x'"/><xsl:template match="/">`
		end := `</xsl:template></xsl:stylesheet>`
		xsltExample(t, p, b, c, `<r><s>A</s></r>`, root+`<xsl:value-of select="key('k','x')"/>|<xsl:for-each select="document('')"><xsl:value-of select="key('k','x')"/>|<xsl:value-of select="name(/*)"/></xsl:for-each>`+end, "A|A|xsl:stylesheet")
		xsltExample(t, p, b, c, `<r><s>A</s></r>`, root+`<xsl:for-each select="document('')"><xsl:value-of select="key('k','x')"/>|<xsl:value-of select="name(key('k','x'))"/>|<xsl:value-of select="name(/*)"/></xsl:for-each>|<xsl:value-of select="key('k','x')"/>`+end, "A|s|xsl:stylesheet|A")
		xsltExample(t, p, b, c, `<r><s>A</s></r>`, root+`<xsl:variable name="id" select="generate-id(/r/s)"/><xsl:for-each select="document('')"><xsl:value-of select="generate-id(key('k','x'))=$id"/>|<xsl:value-of select="generate-id(key('k','x'))=generate-id(/*/d:k)"/></xsl:for-each>`+end, "true|false")
		styleFirst := root + `<xsl:for-each select="document('')"><xsl:value-of select="key('k','x')"/></xsl:for-each>` + end
		xml2Held(t, p, b, c, xsltValue(`<r><s>A</s></r>`, styleFirst, "A"), xsltValue(`<r><s>B</s></r>`, styleFirst, "B"))
		xsltExample(t, p, b, c, `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="count(document('',/))"/>|<xsl:value-of select="count(document('',/missing))"/>|<xsl:value-of select="generate-id(/)=generate-id(document(''))"/>`), "1|1|false")
		xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("apply", "Invalid type"), false, `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:variable name="v"><a>A</a></xsl:variable><xsl:value-of select="$v/a"/>`))
	})
}
