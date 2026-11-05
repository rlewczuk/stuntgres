package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xsltTextOutput = `<xsl:output method="text" encoding="UTF-8"/>`
const xsltTwoQuery = "SELECT xslt_process($1::text,$2::text) AS v"
const xsltThreeQuery = "SELECT xslt_process($1::text,$2::text,$3::text) AS v"

func xsltValue(source, style, want any) fixture.Step {
	return fixture.Select(xsltTwoQuery, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, source, style)
}

func xsltParamValue(source, style, params, want any) fixture.Step {
	return fixture.Select(xsltThreeQuery, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, source, style, params)
}

func xsltExample(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source, style, want string) {
	t.Helper()
	s := xsltValue(source, style, want)
	xml2Both(t, p, b, c, s)
	xml2Held(t, p, b, c, s)
}

func TestReferenceXML2XSLTTemplates(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ name, source, declarations, body, want string }{
			{"builtins", `<r>A<n>B</n><!--C--><?p D?></r>`, ``, `<xsl:apply-templates/>`, "AB"},
			{"predicates_priority", `<r><n/><n x="1"/></r>`, `<xsl:template match="n">N</xsl:template><xsl:template match="n[@x]">P</xsl:template><xsl:template match="*">W</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "NP"},
			{"explicit_priority", `<r><n/><n x="1"/></r>`, `<xsl:template match="n" priority="2">N</xsl:template><xsl:template match="n[@x]">P</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "NN"},
			{"modes", `<r><n>A</n><n>B</n></r>`, `<xsl:template match="n">D</xsl:template><xsl:template match="n" mode="m">[<xsl:value-of select="."/>]</xsl:template>`, `<xsl:apply-templates select="r/n" mode="m"/>`, "[A][B]"},
			{"named_retains_list", `<r><n>A</n><n>B</n></r>`, `<xsl:template name="emit"><xsl:value-of select="concat(position(), '/', last(), ':', .)"/></xsl:template>`, `<xsl:for-each select="r/n"><xsl:call-template name="emit"/></xsl:for-each>`, "1/2:A2/2:B"},
			{"union_branches", `<r><n>A</n><m>B</m><q>C</q></r>`, `<xsl:template match="n|m">U</xsl:template><xsl:template match="*">W</xsl:template>`, `<xsl:apply-templates select="r/*"/>`, "UUW"},
			{"sibling_pattern_context", `<r><n>A</n><n>B</n><n>C</n></r>`, `<xsl:template match="n[1]">F</xsl:template><xsl:template match="n">N</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "FNN"},
			{"attribute_and_pi_patterns", `<r a="A"><?p P?><?q Q?></r>`, `<xsl:template match="@a">A</xsl:template><xsl:template match="processing-instruction('p')">P</xsl:template><xsl:template match="processing-instruction()">I</xsl:template>`, `<xsl:apply-templates select="r/@*|r/processing-instruction()"/>`, "API"},
			{"descendant_path", `<r><a><n>A</n></a><n>B</n></r>`, `<xsl:template match="r//n">D</xsl:template><xsl:template match="r/n">C</xsl:template>`, `<xsl:apply-templates select="r//n"/>`, "DC"},
			{"namespace_wildcard", `<r xmlns:p="urn:p"><p:n/><n/></r>`, `<xsl:template xmlns:p="urn:p" match="p:*">P</xsl:template><xsl:template match="*">W</xsl:template>`, `<xsl:apply-templates select="r/*"/>`, "PW"},
			{"key_pattern", `<r><n id="a"/><n id="b"/></r>`, `<xsl:key name="k" match="n" use="@id"/><xsl:template match="key('k','b')">K</xsl:template><xsl:template match="n">N</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "NK"},
			{"id_pattern", `<!DOCTYPE r [<!ATTLIST n id ID #REQUIRED>]><r><n id="a"/><n id="b"/></r>`, `<xsl:template match="id('b')">I</xsl:template><xsl:template match="n">N</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "NI"},
			{"tie_last", `<r><n/></r>`, `<xsl:template match="n">A</xsl:template><xsl:template match="n">B</xsl:template>`, `<xsl:apply-templates select="r/n"/>`, "B"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xsltExample(t, p, b, c, tc.source, xsltSmokeStyle(xsltTextOutput, tc.declarations, tc.body), tc.want)
			})
		}
		xsltExample(t, p, b, c, `<r/>`, `<t:transform xmlns:t="http://www.w3.org/1999/XSL/Transform" version="1.0"><t:output method="text"/><t:template match="/">ALT</t:template></t:transform>`, "ALT")
		xsltExample(t, p, b, c, `<r/>`, `<out xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xsl:version="1.0">T</out>`, "<?xml version=\"1.0\"?>\n<out>T</out>\n")
	})
}
