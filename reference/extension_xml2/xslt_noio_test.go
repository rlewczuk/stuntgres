package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTNoIO(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		read := xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="/r"/>`)
		// The external-entity reader is genuinely empty; internal entities remain
		// expanded. These are not runtime document() security failures.
		for _, uri := range []string{"file:///stuntgres-xslt-no-such-file.xml", "http://127.0.0.1:9/stuntgres-xslt.xml"} {
			xsltExample(t, p, b, c, `<!DOCTYPE r [<!ENTITY i "I"><!ENTITY e SYSTEM "`+uri+`">]><r>&i;&e;</r>`, read, "I")
			xsltExample(t, p, b, c, `<!DOCTYPE r SYSTEM "`+uri+`"><r>T</r>`, read, "T")
			style := `<!DOCTYPE xsl:stylesheet [<!ENTITY i "I"><!ENTITY e SYSTEM "` + uri + `">]>` + xsltSmokeStyle(xsltTextOutput, "", `<xsl:text>&i;&e;</xsl:text>`)
			xsltExample(t, p, b, c, `<r/>`, style, "I")
			for _, instruction := range []string{"include", "import"} {
				style := xsltSmokeStyle("", `<xsl:`+instruction+` href="`+uri+`"/>`+xsltTextOutput, `T`)
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("compile", "line 1: Document is empty\n\n^"), false, `<r/>`, style)
			}
			style = xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="document('`+uri+`')"/>`)
			xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("apply", ""), false, `<r/>`, style)
			xsltExample(t, p, b, c, `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:if test="false()"><xsl:value-of select="document('`+uri+`')"/></xsl:if>T`), "T")
		}
		for _, uri := range []string{"file:///tmp/stuntgres-xslt-denied/output.xml", "http://127.0.0.1:9/stuntgres-xslt.xml"} {
			style := xsltSmokeStyle(xsltTextOutput, "", `<xsl:document href="`+uri+`"><out>T</out></xsl:document>`)
			xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("apply", ""), false, `<r/>`, style)
		}
		for _, href := range []string{"", "#missing"} {
			detail := ""
			if href != "" {
				detail = "line 1: Document is empty\n\n^"
			}
			xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("compile", detail), false, `<r/>`, xsltSmokeStyle(xsltTextOutput, `<xsl:include href="`+href+`"/>`, `T`))
		}
		xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("apply", ""), false, `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="document('#missing')"/>`))
		// Security still permits the actual in-memory stylesheet, including its
		// data and lexical root. An empty or substituted source tree cannot pass.
		style := `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:d="urn:data">` + xsltTextOutput + `<d:v>S</d:v><xsl:template match="/"><xsl:value-of select="document('')/*/d:v"/>|<xsl:value-of select="name(document('')/*)"/>|<xsl:value-of select="name(/*)"/></xsl:template></xsl:stylesheet>`
		xsltExample(t, p, b, c, `<source/>`, style, "S|xsl:stylesheet|source")
		xml2Held(t, p, b, c, xsltValue(`<one/>`, style, "S|xsl:stylesheet|one"), xsltValue(`<two/>`, strings.Replace(style, "<d:v>S</d:v>", "<d:v>T</d:v>", 1), "T|xsl:stylesheet|two"))
		xml2Held(t, p, b, c, xsltValue(`<r>A</r>`, read, "A"), xsltValue(`<r>B</r>`, read, "B"))
	})
}
